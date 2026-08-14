// Package sidecar defines the aggregation schema whodunit writes into a
// shared database, and the rows it maps local data onto.
//
// # Why a sidecar
//
// The tables live in whodunit's own namespace and never touch DevLake's
// domain tables (NAV-37). DevLake's domain schema shifts between minor
// versions; writing into it would make every DevLake upgrade a potential
// data-loss event for us, and would make us a suspect whenever their own
// data looked wrong.
//
// # Shape
//
// Two grains, deliberately (NAV-53):
//
//   - commits — one row per commit, wide and denormalised. This is what
//     dashboards read. Every column is derivable from the detail tables,
//     so it is a materialised view in all but name.
//   - events and event_lines — the journal, mirrored. This is what makes
//     questions answerable that the trailer alone cannot answer: which
//     files an agent touched that never reached a commit, per-file
//     attribution, work that was abandoned.
//
// Keeping both means a Grafana panel is a single-table scan rather than a
// join across per-edit events, while the detail remains available for
// anything the summary did not anticipate.
//
// # Identity
//
// contributor lives on repos, not on every commit row: a repository has
// one contributor locally, so repeating it per row would be storage spent
// on a constant.
//
// The identity is the git committer email, which is already in every
// commit object. The column adds convenience for querying, not new
// surveillance capability — anyone with the repository can already
// determine who committed what. What it does add is the ability to compare
// *how people work*, which git does not record. That is a real capability
// and the reason this schema is honest about holding identity rather than
// pretending otherwise.
package sidecar

// SchemaVersion is bumped when the table definitions change in a way that
// requires attention. It is stored on every synced row so a reader can
// tell which definition produced a number, months later.
const SchemaVersion = 1

// TablePrefix namespaces every table. DevLake shares the database, so
// unprefixed names would eventually collide with theirs or a plugin's.
const TablePrefix = "whodunit_"

// Schema is the DDL, written to be readable by someone auditing what this
// tool stores about them. Every column is either derived from git or from
// the local journal; nothing is inferred about a person.
//
// Written for both SQLite and MySQL, which DevLake uses. Types come from
// the intersection both accept with the same meaning — VARCHAR, BIGINT,
// REAL. Note MySQL rejects DEFAULT on TEXT columns while SQLite allows it,
// which is why bounded identifiers are VARCHAR rather than TEXT.
//
// Indexes are separate (see Indexes): MySQL has no
// CREATE INDEX IF NOT EXISTS, and inline INDEX clauses are MySQL-only.
const Schema = `
-- One row per repository. Holds facts that do not vary per commit.
CREATE TABLE IF NOT EXISTS whodunit_repos (
	repo_id      VARCHAR(64)  NOT NULL,
	contributor  VARCHAR(320) NOT NULL DEFAULT '',
	spec_version VARCHAR(16)  NOT NULL DEFAULT '',
	synced_at    BIGINT       NOT NULL,
	PRIMARY KEY (repo_id)
);

-- One row per commit: the dashboard grain.
--
-- status and method are stored as written in the trailer rather than as
-- an enum id, so a reader never needs a lookup table to understand a row,
-- and an unrecognised future value survives instead of being dropped.
--
-- ratio is nullable on purpose. A commit with no line-level evidence has
-- no share to report, and 0.0 would assert the agent contributed nothing
-- (NAV-8).
CREATE TABLE IF NOT EXISTS whodunit_commits (
	commit_sha    VARCHAR(64)  NOT NULL,
	repo_id       VARCHAR(64)  NOT NULL,
	committed_at  BIGINT       NOT NULL,
	status        VARCHAR(32)  NOT NULL,
	method        VARCHAR(32)  NOT NULL,
	agent         VARCHAR(64)  NOT NULL DEFAULT '',
	agent_version VARCHAR(64)  NOT NULL DEFAULT '',
	purpose       VARCHAR(32)  NOT NULL DEFAULT '',
	ratio         REAL,
	lines_added   BIGINT       NOT NULL DEFAULT 0,
	lines_removed BIGINT       NOT NULL DEFAULT 0,
	files_changed BIGINT       NOT NULL DEFAULT 0,
	spec_version  VARCHAR(16)  NOT NULL DEFAULT '',
	schema_version BIGINT      NOT NULL,
	synced_at     BIGINT       NOT NULL,
	PRIMARY KEY (commit_sha, repo_id)
);

-- One row per observed agent edit: the journal, mirrored.
--
-- This is the grain that answers what the trailer cannot — an agent
-- touching a file that never reached a commit leaves a row here and
-- nothing in whodunit_commits.
CREATE TABLE IF NOT EXISTS whodunit_events (
	-- Identity is a hash of (repo, session, timestamp, tool, file, hunk),
	-- not those columns directly. A file path can be 512 characters, and
	-- MySQL caps a key at 3072 bytes — four such columns exceed it. The
	-- hash also makes re-syncing the same event a no-op, which is what
	-- keeps sync idempotent.
	event_id      VARCHAR(64)  NOT NULL,
	repo_id       VARCHAR(64)  NOT NULL,
	observed_at   BIGINT       NOT NULL,
	agent         VARCHAR(64)  NOT NULL,
	agent_version VARCHAR(64)  NOT NULL DEFAULT '',
	session       VARCHAR(128) NOT NULL DEFAULT '',
	event         VARCHAR(32)  NOT NULL,
	tool          VARCHAR(64)  NOT NULL DEFAULT '',
	file          VARCHAR(512) NOT NULL DEFAULT '',
	lines_added   BIGINT       NOT NULL DEFAULT 0,
	lines_removed BIGINT       NOT NULL DEFAULT 0,
	hunk_hash     VARCHAR(80)  NOT NULL DEFAULT '',
	spec_version  VARCHAR(16)  NOT NULL DEFAULT '',
	outcome       VARCHAR(16)  NOT NULL DEFAULT '',
	synced_at     BIGINT       NOT NULL,

	-- NULLable, unlike everything above, and deliberately so (NAV-88).
	--
	-- The columns above default to '' or 0 because every agent supplies
	-- them. These cannot be: agy records no branch at all, and only Claude
	-- Code reports whether a human edited the agent's output. A default
	-- would assert "measured, and it was empty" about something never
	-- measurable, and nothing downstream could tell the two apart
	-- afterwards (NAV-21).
	--
	-- A panel reading these must render NULL as "not reported by this
	-- agent", never as zero.
	model         VARCHAR(64),
	branch        VARCHAR(255),
	mcp_server    VARCHAR(128),
	user_modified TINYINT,

	PRIMARY KEY (event_id)
);

-- Engagement per session (NAV-55): counts only, never message content.
-- A message count needs no message text, which is what keeps this
-- compatible with the no-prompt-text rule.
CREATE TABLE IF NOT EXISTS whodunit_sessions (
	repo_id        VARCHAR(64)  NOT NULL,
	session        VARCHAR(128) NOT NULL,
	agent          VARCHAR(64)  NOT NULL DEFAULT '',
	agent_version  VARCHAR(64)  NOT NULL DEFAULT '',
	first_seen     BIGINT       NOT NULL,
	last_seen      BIGINT       NOT NULL,
	user_messages  BIGINT       NOT NULL DEFAULT 0,
	agent_messages BIGINT       NOT NULL DEFAULT 0,
	tool_calls     BIGINT       NOT NULL DEFAULT 0,
	distinct_tools BIGINT       NOT NULL DEFAULT 0,
	mcp_calls      BIGINT       NOT NULL DEFAULT 0,
	synced_at      BIGINT       NOT NULL,

	-- Measured cost, timing and autonomy (NAV-88). All NULLable: agy
	-- supplies none of them — verified genuinely absent rather than merely
	-- unread — and only Codex separates reasoning tokens or records
	-- timing at all. Two agents out of three leave those NULL permanently,
	-- which is a fact about the agents rather than a gap awaiting work.
	--
	-- Zero would be the wrong default here in a specific and expensive
	-- way: on a cost panel it reads as "this agent is free".
	input_tokens           BIGINT,
	output_tokens          BIGINT,
	cache_read_tokens      BIGINT,
	cache_write_tokens     BIGINT,
	reasoning_tokens       BIGINT,
	duration_ms            BIGINT,
	time_to_first_token_ms BIGINT,
	effort                 VARCHAR(16),
	permission_mode        VARCHAR(32),
	model                  VARCHAR(64),
	compactions            BIGINT,

	PRIMARY KEY (repo_id, session)
);

-- One row per captured pre-adoption baseline (NAV-107).
--
-- The comparison this table exists for is the only honest one available:
-- the same repository before and after, with the selection problem absent
-- by construction. Assisted against unassisted commits in the same period
-- is a different and weaker question, because an agent is reached for on
-- some kinds of work and not others.
--
-- Keyed by repo_id and captured_at rather than repo_id alone. A snapshot
-- is immutable once written locally, so a second one with a different
-- capture time is a NEW baseline — recapturing after a --force, or a
-- different window — and overwriting silently would destroy the record of
-- what was actually compared.
--
-- manual_* are nullable throughout: they come from PR and CI systems this
-- tool cannot see, and are absent in every snapshot captured so far. A
-- zero would assert a measured zero (NAV-21).
CREATE TABLE IF NOT EXISTS whodunit_baselines (
	repo_id             VARCHAR(64)  NOT NULL,
	captured_at         BIGINT       NOT NULL,
	window_days         BIGINT       NOT NULL,
	head_sha            VARCHAR(64)  NOT NULL DEFAULT '',
	schema_version      VARCHAR(16)  NOT NULL DEFAULT '',

	commits             BIGINT       NOT NULL DEFAULT 0,
	commits_per_week    REAL,
	median_diff_lines   BIGINT,
	mean_hours_between  REAL,
	reverts             BIGINT,
	revert_rate         REAL,

	synced_at           BIGINT       NOT NULL,
	PRIMARY KEY (repo_id, captured_at)
);

-- Hashes of lines an agent produced (NAV-52).
--
-- Only hashes, never the lines. A hash cannot be read back into code, so
-- this table cannot reconstruct anyone's source — which is what makes it
-- safe to hold centrally at all.
CREATE TABLE IF NOT EXISTS whodunit_event_lines (
	repo_id   VARCHAR(64) NOT NULL,
	line_hash BIGINT      NOT NULL,
	first_at  BIGINT      NOT NULL,
	synced_at BIGINT      NOT NULL,
	PRIMARY KEY (repo_id, line_hash)
);
`

// Indexes are applied separately from Schema because the two engines
// disagree about how to declare them idempotently: SQLite supports
// CREATE INDEX IF NOT EXISTS but not inline INDEX clauses, and MySQL is
// exactly the reverse.
//
// A caller applies these and tolerates failure. The only realistic error is
// "already exists", and an index that could not be created costs query
// speed rather than correctness — not a reason to fail a sync.
// Migrations bring a table created by an earlier version up to date.
//
// CREATE TABLE IF NOT EXISTS silently does nothing when the table already
// exists, so a new column never appears on a database that has already
// been synced to. Each statement is applied best-effort: the expected
// failure is "column already exists", which is the desired end state.
// Every statement here has to be valid on BOTH SQLite and MySQL, which is
// narrower than either alone and has bitten this project before. Kept to
// the intersection deliberately:
//
//   - one ADD COLUMN per statement. MySQL accepts several in one ALTER,
//     SQLite does not.
//   - no AFTER <column>. MySQL only.
//   - no IF NOT EXISTS on ADD COLUMN. Neither engine supports it in the
//     version range this targets, which is why these are best-effort.
//   - BIGINT and VARCHAR(n) rather than INTEGER/TEXT. SQLite accepts both
//     under its type affinity rules; MySQL needs the length.
var Migrations = []string{
	`ALTER TABLE whodunit_events ADD COLUMN outcome VARCHAR(16) NOT NULL DEFAULT ''`,

	// NAV-88. NULLable, unlike the columns declared in Schema above.
	//
	// Those default to '' or 0 because every agent can supply them. These
	// cannot — agy records no branch at all, and only Claude Code reports
	// whether a human edited the agent's output. Writing '' would assert
	// "measured, and it was empty" about something never measurable, and
	// nothing downstream could tell that apart afterwards (NAV-21).
	//
	// A dashboard panel reading these must render NULL as "not reported by
	// this agent", never as zero — a zero on a cost panel reads as "this
	// agent is free".
	`ALTER TABLE whodunit_events ADD COLUMN model VARCHAR(64)`,
	`ALTER TABLE whodunit_events ADD COLUMN branch VARCHAR(255)`,
	`ALTER TABLE whodunit_events ADD COLUMN mcp_server VARCHAR(128)`,
	`ALTER TABLE whodunit_events ADD COLUMN user_modified TINYINT`,

	// Per-session measurements. agy supplies none of them — verified
	// absent rather than merely unread — so they stay NULL for every agy
	// session rather than reporting an agent that costs nothing.
	`ALTER TABLE whodunit_sessions ADD COLUMN input_tokens BIGINT`,
	`ALTER TABLE whodunit_sessions ADD COLUMN output_tokens BIGINT`,
	`ALTER TABLE whodunit_sessions ADD COLUMN cache_read_tokens BIGINT`,
	`ALTER TABLE whodunit_sessions ADD COLUMN cache_write_tokens BIGINT`,

	// Codex alone separates reasoning tokens and records timing, so two
	// agents out of three leave these NULL permanently. That is a fact
	// about the agents, not a gap awaiting work.
	`ALTER TABLE whodunit_sessions ADD COLUMN reasoning_tokens BIGINT`,
	`ALTER TABLE whodunit_sessions ADD COLUMN duration_ms BIGINT`,
	`ALTER TABLE whodunit_sessions ADD COLUMN time_to_first_token_ms BIGINT`,

	`ALTER TABLE whodunit_sessions ADD COLUMN effort VARCHAR(16)`,
	`ALTER TABLE whodunit_sessions ADD COLUMN permission_mode VARCHAR(32)`,
	`ALTER TABLE whodunit_sessions ADD COLUMN model VARCHAR(64)`,

	// NAV-106. Compactions per session — NULL for agy, which has no
	// equivalent signal.
	`ALTER TABLE whodunit_sessions ADD COLUMN compactions BIGINT`,
}

var Indexes = []string{
	`CREATE INDEX idx_whodunit_commits_repo_time ON whodunit_commits (repo_id, committed_at)`,
	`CREATE INDEX idx_whodunit_commits_method ON whodunit_commits (repo_id, method)`,
	`CREATE INDEX idx_whodunit_events_repo_time ON whodunit_events (repo_id, observed_at)`,
	`CREATE INDEX idx_whodunit_events_file ON whodunit_events (repo_id, file)`,
}
