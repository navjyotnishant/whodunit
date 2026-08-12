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
// Written for both SQLite (local DevLake) and MySQL (DevLake's default).
// Types are chosen from the intersection that both accept with the same
// meaning: TEXT, INTEGER, REAL.
const Schema = `
-- One row per repository. Holds facts that do not vary per commit.
CREATE TABLE IF NOT EXISTS whodunit_repos (
	repo_id      TEXT NOT NULL,
	contributor  TEXT NOT NULL DEFAULT '',
	spec_version TEXT NOT NULL DEFAULT '',
	synced_at    INTEGER NOT NULL,
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
	commit_sha    TEXT NOT NULL,
	repo_id       TEXT NOT NULL,
	committed_at  INTEGER NOT NULL,
	status        TEXT NOT NULL,
	method        TEXT NOT NULL,
	agent         TEXT NOT NULL DEFAULT '',
	agent_version TEXT NOT NULL DEFAULT '',
	purpose       TEXT NOT NULL DEFAULT '',
	ratio         REAL,
	lines_added   INTEGER NOT NULL DEFAULT 0,
	lines_removed INTEGER NOT NULL DEFAULT 0,
	files_changed INTEGER NOT NULL DEFAULT 0,
	spec_version  TEXT NOT NULL DEFAULT '',
	schema_version INTEGER NOT NULL,
	synced_at     INTEGER NOT NULL,
	PRIMARY KEY (commit_sha, repo_id)
);
CREATE INDEX IF NOT EXISTS idx_whodunit_commits_repo_time
	ON whodunit_commits (repo_id, committed_at);
CREATE INDEX IF NOT EXISTS idx_whodunit_commits_method
	ON whodunit_commits (repo_id, method);

-- One row per observed agent edit: the journal, mirrored.
--
-- This is the grain that answers what the trailer cannot — an agent
-- touching a file that never reached a commit leaves a row here and
-- nothing in whodunit_commits.
CREATE TABLE IF NOT EXISTS whodunit_events (
	repo_id       TEXT NOT NULL,
	observed_at   INTEGER NOT NULL,
	agent         TEXT NOT NULL,
	agent_version TEXT NOT NULL DEFAULT '',
	session       TEXT NOT NULL DEFAULT '',
	event         TEXT NOT NULL,
	tool          TEXT NOT NULL DEFAULT '',
	file          TEXT NOT NULL DEFAULT '',
	lines_added   INTEGER NOT NULL DEFAULT 0,
	lines_removed INTEGER NOT NULL DEFAULT 0,
	hunk_hash     TEXT NOT NULL DEFAULT '',
	spec_version  TEXT NOT NULL DEFAULT '',
	synced_at     INTEGER NOT NULL,
	PRIMARY KEY (repo_id, session, observed_at, tool, file, hunk_hash)
);
CREATE INDEX IF NOT EXISTS idx_whodunit_events_repo_time
	ON whodunit_events (repo_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_whodunit_events_file
	ON whodunit_events (repo_id, file);

-- Hashes of lines an agent produced (NAV-52).
--
-- Only hashes, never the lines. A hash cannot be read back into code, so
-- this table cannot reconstruct anyone's source — which is what makes it
-- safe to hold centrally at all.
CREATE TABLE IF NOT EXISTS whodunit_event_lines (
	repo_id   TEXT NOT NULL,
	line_hash INTEGER NOT NULL,
	first_at  INTEGER NOT NULL,
	synced_at INTEGER NOT NULL,
	PRIMARY KEY (repo_id, line_hash)
);
`
