// Package journal implements the local-only observation log (SPEC v0.2),
// backed by an embedded SQLite database — single file, no server process,
// no CGO (pure-Go driver, so cross-compilation for release binaries stays
// simple).
//
// The store is global (one database under ~/.whodunit) and rows are scoped
// by repo_id rather than by which file they live in. Scoping by column
// instead of by path is what makes a future move to Postgres or Mongo a
// driver change rather than a redesign: a shared server has one table for
// everything, so the repo has to be a value in the row either way.
//
// repo_id is the repository's root commit SHA (see internal/repoid) — stable
// across clones, machines, and paths, and revealing nothing on its own,
// unlike a filesystem path or a remote URL.
//
// Hard constraints, non-negotiable:
//   - No network calls, ever.
//   - Never writes to any git repository.
//   - No names, emails, hostnames, remote URLs, prompt text, or file contents.
package journal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	_ "modernc.org/sqlite"
)

// Entry is one observation event. Field set is deliberately minimal —
// see the package doc for what must never appear here. JSON tags exist for
// stable `dun journal show` output, not for storage — storage is SQLite.
type Entry struct {
	Timestamp    time.Time `json:"ts"`
	Agent        string    `json:"agent"`
	AgentVersion string    `json:"agent_version"`
	Session      string    `json:"session"`
	Event        string    `json:"event"` // tool_use | session_started | session_ended
	Tool         string    `json:"tool,omitempty"`
	File         string    `json:"file,omitempty"`
	LinesAdded   int       `json:"lines_added,omitempty"`
	LinesRemoved int       `json:"lines_removed,omitempty"`
	HunkHash     string    `json:"hunk_hash,omitempty"`
	SpecVersion  string    `json:"spec_version"`

	// Outcome is what happened to the tool call: accepted, rejected,
	// failed, or unknown (NAV-54). A rejected call is a human declining,
	// which is the denominator an acceptance rate needs; a failed one is
	// the tool erroring, which is a different thing entirely.
	Outcome string `json:"outcome,omitempty"`

	// Model, Branch and MCPServer are observations about the edit rather
	// than part of its identity — they are deliberately outside the UNIQUE
	// constraint, so re-ingesting an edit after a branch rename updates the
	// row instead of inserting a second one (NAV-88).
	//
	// Empty means the agent does not report it, and that is agent-specific
	// rather than incidental: agy records no branch at all, verified absent
	// rather than merely unread.
	Model     string `json:"model,omitempty"`
	Branch    string `json:"branch,omitempty"`
	MCPServer string `json:"mcp_server,omitempty"`

	// UserModified is whether a human edited the agent's output before it
	// was committed — the one signal that separates "the agent wrote this"
	// from "the agent wrote this and it was kept".
	//
	// A pointer because three states matter and a bool carries two: true,
	// false, and "this agent cannot tell us". Only Claude Code reports it;
	// Codex and agy have no equivalent, so nil there is permanent rather
	// than pending (NAV-21).
	UserModified *bool `json:"user_modified,omitempty"`

	// LineHashes are the hashes of individual lines this event produced
	// (NAV-52). Not serialized in `dun journal show`: there can be hundreds
	// per event, and they are lookup keys rather than something a human
	// reads. They are stored in their own table, not on the entry row.
	LineHashes []uint64 `json:"-"`
}

// SpecVersion is the journal schema version this package writes.
const SpecVersion = "0.2"

const schema = `
CREATE TABLE IF NOT EXISTS entries (
	repo_id       TEXT NOT NULL,
	ts            INTEGER NOT NULL,
	agent         TEXT NOT NULL,
	agent_version TEXT NOT NULL,
	session       TEXT NOT NULL,
	event         TEXT NOT NULL,
	tool          TEXT NOT NULL DEFAULT '',
	file          TEXT NOT NULL DEFAULT '',
	lines_added   INTEGER NOT NULL DEFAULT 0,
	lines_removed INTEGER NOT NULL DEFAULT 0,
	hunk_hash     TEXT NOT NULL DEFAULT '',
	spec_version  TEXT NOT NULL,
	outcome       TEXT NOT NULL DEFAULT '',

	-- NULLable, unlike everything above (NAV-88).
	--
	-- Those default to '' or 0 because every agent supplies them. These
	-- cannot be: agy records no branch at all, and only Claude Code
	-- reports whether a human edited the agent's output. A default asserts
	-- "measured, and it was empty" about something never measurable, and
	-- nothing downstream can tell the two apart afterwards (NAV-21).
	model         TEXT,
	branch        TEXT,
	mcp_server    TEXT,
	user_modified INTEGER,

	UNIQUE(repo_id, session, ts, tool, file, hunk_hash)
);
CREATE INDEX IF NOT EXISTS idx_entries_repo_ts ON entries(repo_id, ts);
CREATE INDEX IF NOT EXISTS idx_entries_repo_file ON entries(repo_id, file);

-- One row per distinct line an agent produced, scoped by repository
-- (NAV-52). Separate from entries because the relationship is one entry to
-- many lines, and because the primary key deduplicates for free: an agent
-- rewriting the same block a dozen times contributes those lines once.
--
-- Only the hash is stored, never the line itself. The journal must not
-- hold file contents, and a hash cannot be read back into code.
CREATE TABLE IF NOT EXISTS agent_lines (
	repo_id   TEXT NOT NULL,
	line_hash INTEGER NOT NULL,
	first_ts  INTEGER NOT NULL,
	PRIMARY KEY (repo_id, line_hash)
);
CREATE INDEX IF NOT EXISTS idx_agent_lines_repo_ts ON agent_lines(repo_id, first_ts);

-- Facts about a repository's journal rather than observations within it:
-- one row per repository, not one per event. The contributor lives here
-- rather than on every entry because locally it is always the same person,
-- so repeating it per row would be storage spent on a constant.
--
-- Central aggregation joins on this rather than transforming per row: a
-- sync reads one metadata row and one set of entries.
-- Engagement per session: how much conversation and tool use it held
-- (NAV-55). Counts only — no message text, no tool arguments. A message
-- count needs no message content.
--
-- Its own table because the grain is per session, while entries are per
-- tool call.
CREATE TABLE IF NOT EXISTS sessions (
	repo_id        TEXT NOT NULL,
	session        TEXT NOT NULL,
	agent          TEXT NOT NULL DEFAULT '',
	agent_version  TEXT NOT NULL DEFAULT '',
	first_seen     INTEGER NOT NULL,
	last_seen      INTEGER NOT NULL,
	user_messages  INTEGER NOT NULL DEFAULT 0,
	agent_messages INTEGER NOT NULL DEFAULT 0,
	tool_calls     INTEGER NOT NULL DEFAULT 0,
	distinct_tools INTEGER NOT NULL DEFAULT 0,
	mcp_calls      INTEGER NOT NULL DEFAULT 0,

	-- Measured cost, timing and autonomy (NAV-88), all NULLable. agy
	-- supplies none of them, and only Codex separates reasoning tokens or
	-- records timing — so two agents out of three leave those NULL
	-- permanently. Zero would read as "this agent is free".
	input_tokens           INTEGER,
	output_tokens          INTEGER,
	cache_read_tokens      INTEGER,
	cache_write_tokens     INTEGER,
	reasoning_tokens       INTEGER,
	duration_ms            INTEGER,
	time_to_first_token_ms INTEGER,
	effort                 TEXT,
	permission_mode        TEXT,
	model                  TEXT,
	compactions            INTEGER,

	PRIMARY KEY (repo_id, session)
);

CREATE TABLE IF NOT EXISTS repo_metadata (
	repo_id      TEXT PRIMARY KEY,
	contributor  TEXT NOT NULL DEFAULT '',
	spec_version TEXT NOT NULL,
	updated_at   INTEGER NOT NULL
);
`

// migrations are columns added to tables that may already exist on disk.
//
// Each is applied unconditionally and its error ignored: SQLite has no
// ADD COLUMN IF NOT EXISTS, and "duplicate column name" is the expected
// result on every run after the first. A genuinely broken statement would
// surface as a failing read immediately afterwards.
var migrations = []string{
	`ALTER TABLE entries ADD COLUMN outcome TEXT NOT NULL DEFAULT ''`,

	// NAV-88. Deliberately NULLable, unlike every column above.
	//
	// The existing columns default to '' or 0 because every agent can
	// supply them. These cannot: agy records no branch at all, and neither
	// Codex nor agy reports whether a human edited the agent's output. A
	// NOT NULL DEFAULT '' would write "measured, and it was empty" for a
	// field that was never measurable — which is precisely the confusion
	// NAV-21 exists to prevent, and it is unrecoverable once written,
	// because nothing downstream can tell the two apart afterwards.
	//
	// So: NULL means "this agent cannot tell us". A value means we looked.
	`ALTER TABLE entries ADD COLUMN model TEXT`,
	`ALTER TABLE entries ADD COLUMN branch TEXT`,
	`ALTER TABLE entries ADD COLUMN mcp_server TEXT`,

	// Whether a human edited the agent's output before it was committed.
	// Claude Code alone reports it (toolUseResult.userModified), so for the
	// other two this stays NULL rather than false — "nobody edited it" and
	// "we cannot see edits" are different claims, and the second must not
	// be reported as the first.
	`ALTER TABLE entries ADD COLUMN user_modified INTEGER`,

	// Per-session measurements (NAV-88), NULLable for the same reason.
	//
	// Token counts: Claude Code carries usage on 100% of assistant turns,
	// Codex carries it in event_msg/token_count. agy has none — verified
	// absent rather than merely unread, so every one of these stays NULL
	// there. A 0 would report an agent that costs nothing.
	`ALTER TABLE sessions ADD COLUMN input_tokens INTEGER`,
	`ALTER TABLE sessions ADD COLUMN output_tokens INTEGER`,
	`ALTER TABLE sessions ADD COLUMN cache_read_tokens INTEGER`,
	`ALTER TABLE sessions ADD COLUMN cache_write_tokens INTEGER`,

	// Codex alone separates reasoning tokens, and Codex alone records
	// timing. Two agents out of three will always leave these NULL, which
	// is a fact about the agents rather than a gap to be filled in later.
	`ALTER TABLE sessions ADD COLUMN reasoning_tokens INTEGER`,
	`ALTER TABLE sessions ADD COLUMN duration_ms INTEGER`,
	`ALTER TABLE sessions ADD COLUMN time_to_first_token_ms INTEGER`,

	// How much autonomy the agent was given, and how hard it was asked to
	// think. Enums rather than counts, so they stay text.
	`ALTER TABLE sessions ADD COLUMN effort TEXT`,
	`ALTER TABLE sessions ADD COLUMN permission_mode TEXT`,

	// The model that produced the session. On entries as well because a
	// session can change model mid-way, and cost is attributed per turn.
	`ALTER TABLE sessions ADD COLUMN model TEXT`,

	// NAV-106. How often the session's context was compacted — nil where
	// the agent does not report it, which is agy.
	`ALTER TABLE sessions ADD COLUMN compactions INTEGER`,
}

// DBPath returns the journal database location inside the given data
// directory.
func DBPath(dataDir string) string {
	return filepath.Join(dataDir, "journal.db")
}

// dbPerm is owner-only. The journal records which files were edited and
// when; the SQLite driver creates the file 0644 by default, which is
// world-readable on a shared machine.
const dbPerm = 0o600

// tightenDBPerms fixes the database file's mode after the driver creates
// it, and repairs a file created before this rule existed.
func tightenDBPerms(dataDir string) error {
	path := DBPath(dataDir)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Perm() == dbPerm {
		return nil
	}
	return os.Chmod(path, dbPerm)
}

// Writer appends entries for one repository to the global journal.
type Writer struct {
	db     *sql.DB
	repoID string
}

// NewWriter opens (creating if needed) the global journal database in
// dataDir, scoped to repoID. Every entry written through it belongs to that
// repository; nothing can write across repos by accident.
func NewWriter(dataDir, repoID string) (*Writer, error) {
	if repoID == "" {
		return nil, fmt.Errorf("journal: repo id is required")
	}
	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	return &Writer{db: db, repoID: repoID}, nil
}

// Close releases the underlying database handle.
func (w *Writer) Close() error {
	return w.db.Close()
}

// Append writes one entry to the journal. Re-appending an entry already
// present (same repo, session, timestamp, tool, file, and hunk hash) is a
// no-op — ingest can be re-run safely without duplicating history.
func (w *Writer) Append(e Entry) error {
	if e.SpecVersion == "" {
		e.SpecVersion = SpecVersion
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	_, err := w.db.Exec(
		// Outcome is updated on conflict rather than ignored: an entry
		// written before outcomes were collected must be able to gain one,
		// and a call whose result arrives in a later ingest must be able to
		// change from unknown to accepted. Everything else about the row is
		// immutable, so re-ingest stays idempotent.
		// The measured columns are COALESCEd for the same reason the
		// session ones are: a re-ingest over a window that cannot see them
		// must not erase what an earlier pass established.
		`INSERT INTO entries (repo_id, ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome, model, branch, mcp_server, user_modified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repo_id, session, ts, tool, file, hunk_hash) DO UPDATE SET
		   outcome=excluded.outcome,
		   lines_added=excluded.lines_added,
		   lines_removed=excluded.lines_removed,
		   model=COALESCE(excluded.model, entries.model),
		   branch=COALESCE(excluded.branch, entries.branch),
		   mcp_server=COALESCE(excluded.mcp_server, entries.mcp_server),
		   user_modified=COALESCE(excluded.user_modified, entries.user_modified)`,
		w.repoID, e.Timestamp.UnixNano(), e.Agent, e.AgentVersion, e.Session, e.Event,
		e.Tool, e.File, e.LinesAdded, e.LinesRemoved, e.HunkHash, e.SpecVersion, e.Outcome,
		nullString(e.Model), nullString(e.Branch), nullString(e.MCPServer), e.UserModified,
	)
	if err != nil {
		return fmt.Errorf("journal: insert entry: %w", err)
	}
	return nil
}

// Session is engagement for one agent session (NAV-55).
type Session struct {
	Session       string
	Agent         string
	AgentVersion  string
	FirstSeen     time.Time
	LastSeen      time.Time
	UserMessages  int
	AgentMessages int
	ToolCalls     int
	DistinctTools int
	MCPCalls      int

	// Measured cost, timing and autonomy (NAV-88). Pointers, not values,
	// and that is the point: a nil is "this agent does not report it",
	// which is a different claim from zero.
	//
	// An int would make them indistinguishable. agy reports none of these
	// — verified genuinely absent, not merely unread — and only Codex
	// separates reasoning tokens or records timing at all, so a plain int
	// would write 0 for two agents out of three and a cost panel would
	// report that they are free (NAV-21).
	//
	// The schema columns are NULLable for the same reason; these are the
	// in-memory half of that guarantee.
	InputTokens        *int64
	OutputTokens       *int64
	CacheReadTokens    *int64
	CacheWriteTokens   *int64
	ReasoningTokens    *int64
	DurationMS         *int64
	TimeToFirstTokenMS *int64

	// Enums rather than counts: how hard the model was asked to think, and
	// how much autonomy it was given. Empty means not reported.
	Effort         string
	PermissionMode string

	// Compactions is how many times the session's context was compacted
	// (NAV-106).
	//
	// The signal an efficiency panel needs most: a long session costs more
	// even when cached, because the whole context is re-sent every turn,
	// and compacting is the thing a team can actually do about it.
	// Measured on this machine, 92% of turns ran above 150k context while
	// only 4 of 60 sessions ever compacted.
	//
	// nil for agy, which has no equivalent — and zero compactions is a
	// different claim from "this agent cannot tell us" (NAV-21).
	Compactions *int64

	// The model that produced the session. A session can change model
	// part-way through; this records the last one seen, which is what the
	// turn that finished the work used.
	Model string
}

// UpsertSession records or updates one session's activity. A session grows
// as work continues, so a later ingest overwrites the earlier counts rather
// than adding to them.
func (w *Writer) UpsertSession(s Session) error {
	if s.Session == "" {
		return nil
	}
	_, err := w.db.Exec(
		// COALESCE on every measured column, so a re-ingest cannot erase
		// what an earlier one established.
		//
		// The failure without it: `dun ingest --since` reads a narrow
		// window, and a session whose token-bearing turns fall outside it
		// parses with nil tokens. A plain excluded.* would overwrite real
		// measurements with NULL, and the loss is silent and permanent —
		// the transcript may have been rotated away by the time anyone
		// notices the cost column emptied out.
		//
		// COALESCE(excluded.x, sessions.x) takes the new value when there
		// is one and keeps the old when there is not. It cannot un-set a
		// column, which is the right trade: these are measurements of
		// something that happened, and a later narrower read is a smaller
		// view of the same past, not a correction of it.
		//
		// The engagement counts above are NOT coalesced. They are computed
		// for whatever window was read and are meant to be replaced.
		`INSERT INTO sessions (repo_id, session, agent, agent_version, first_seen, last_seen,
		   user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls,
		   input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		   reasoning_tokens, duration_ms, time_to_first_token_ms,
		   effort, permission_mode, model, compactions)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repo_id, session) DO UPDATE SET
		   agent_version=excluded.agent_version, last_seen=excluded.last_seen,
		   user_messages=excluded.user_messages, agent_messages=excluded.agent_messages,
		   tool_calls=excluded.tool_calls, distinct_tools=excluded.distinct_tools,
		   mcp_calls=excluded.mcp_calls,
		   input_tokens=COALESCE(excluded.input_tokens, sessions.input_tokens),
		   output_tokens=COALESCE(excluded.output_tokens, sessions.output_tokens),
		   cache_read_tokens=COALESCE(excluded.cache_read_tokens, sessions.cache_read_tokens),
		   cache_write_tokens=COALESCE(excluded.cache_write_tokens, sessions.cache_write_tokens),
		   reasoning_tokens=COALESCE(excluded.reasoning_tokens, sessions.reasoning_tokens),
		   duration_ms=COALESCE(excluded.duration_ms, sessions.duration_ms),
		   time_to_first_token_ms=COALESCE(excluded.time_to_first_token_ms, sessions.time_to_first_token_ms),
		   effort=COALESCE(excluded.effort, sessions.effort),
		   permission_mode=COALESCE(excluded.permission_mode, sessions.permission_mode),
		   model=COALESCE(excluded.model, sessions.model),
		   compactions=COALESCE(excluded.compactions, sessions.compactions)`,
		w.repoID, s.Session, s.Agent, s.AgentVersion,
		s.FirstSeen.UnixNano(), s.LastSeen.UnixNano(),
		s.UserMessages, s.AgentMessages, s.ToolCalls, s.DistinctTools, s.MCPCalls,
		s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens,
		s.ReasoningTokens, s.DurationMS, s.TimeToFirstTokenMS,
		nullString(s.Effort), nullString(s.PermissionMode), nullString(s.Model),
		s.Compactions)
	if err != nil {
		return fmt.Errorf("journal: upsert session: %w", err)
	}
	return nil
}

// nullString writes an empty string as SQL NULL.
//
// The pointer fields carry their own nil, but Effort, PermissionMode and
// Model are plain strings — an agent that does not report them leaves ""
// rather than nil. Written as-is, "" would land in the column as a value,
// and COALESCE would then treat it as a real measurement and refuse to let
// a later ingest fill it in. It also reads downstream as "measured, and it
// was empty" (NAV-21).
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ReadSessions returns a repository's session activity.
func ReadSessions(dataDir, repoID string) ([]Session, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return nil, nil
	}
	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT session, agent, agent_version, first_seen, last_seen,
		        user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls,
		        input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		        reasoning_tokens, duration_ms, time_to_first_token_ms,
		        effort, permission_mode, model, compactions
		 FROM sessions WHERE repo_id = ? ORDER BY first_seen`, repoID)
	if err != nil {
		return nil, fmt.Errorf("journal: query sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		var first, last int64

		// Scanned through Null* types and only then converted, so a NULL
		// column stays nil on the way out. Scanning straight into *int64
		// would give a non-nil pointer to zero, and the distinction
		// between "reported nothing" and "reported zero" would be lost at
		// the last step after being preserved everywhere else (NAV-21).
		var in, outTok, cacheR, cacheW, reasoning, dur, ttft, compactions sql.NullInt64
		var effort, permission, model sql.NullString

		if err := rows.Scan(&s.Session, &s.Agent, &s.AgentVersion, &first, &last,
			&s.UserMessages, &s.AgentMessages, &s.ToolCalls, &s.DistinctTools, &s.MCPCalls,
			&in, &outTok, &cacheR, &cacheW, &reasoning, &dur, &ttft,
			&effort, &permission, &model, &compactions); err != nil {
			return nil, fmt.Errorf("journal: scan session: %w", err)
		}
		s.FirstSeen = time.Unix(0, first).UTC()
		s.LastSeen = time.Unix(0, last).UTC()

		s.InputTokens = nullInt(in)
		s.OutputTokens = nullInt(outTok)
		s.CacheReadTokens = nullInt(cacheR)
		s.CacheWriteTokens = nullInt(cacheW)
		s.ReasoningTokens = nullInt(reasoning)
		s.DurationMS = nullInt(dur)
		s.TimeToFirstTokenMS = nullInt(ttft)
		s.Compactions = nullInt(compactions)
		s.Effort = effort.String
		s.PermissionMode = permission.String
		s.Model = model.String

		out = append(out, s)
	}
	return out, rows.Err()
}

// nullInt converts a scanned NULL into a nil pointer rather than a pointer
// to zero.
func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// Metadata describes a repository's journal rather than any event within
// it. Central aggregation reads one of these per repository instead of
// carrying the same values on every row.
type Metadata struct {
	RepoID string

	// Contributor is the git committer identity for this repository,
	// captured at `dun init`. Empty locally means it was never captured —
	// a repository initialised before this existed, or one where git has
	// no user.email configured.
	//
	// It is stored once per repository rather than per event because
	// locally it is always the same person: repeating it per row would be
	// storage spent on a constant.
	Contributor string

	SpecVersion string
	UpdatedAt   time.Time
}

// SetMetadata records or updates a repository's metadata.
func SetMetadata(dataDir string, m Metadata) error {
	if m.RepoID == "" {
		return fmt.Errorf("journal: repo id is required")
	}
	if m.SpecVersion == "" {
		m.SpecVersion = SpecVersion
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now().UTC()
	}

	db, err := open(dataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(
		`INSERT INTO repo_metadata (repo_id, contributor, spec_version, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   contributor = excluded.contributor,
		   spec_version = excluded.spec_version,
		   updated_at = excluded.updated_at`,
		m.RepoID, m.Contributor, m.SpecVersion, m.UpdatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("journal: write metadata: %w", err)
	}
	return nil
}

// GetMetadata reads a repository's metadata. Returns (nil, nil) when the
// repository has none — a journal written before metadata existed, or one
// never initialised.
func GetMetadata(dataDir, repoID string) (*Metadata, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return nil, nil
	}

	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var m Metadata
	var updated int64
	err = db.QueryRow(
		`SELECT repo_id, contributor, spec_version, updated_at FROM repo_metadata WHERE repo_id = ?`,
		repoID).Scan(&m.RepoID, &m.Contributor, &m.SpecVersion, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: read metadata: %w", err)
	}
	m.UpdatedAt = time.Unix(0, updated).UTC()
	return &m, nil
}

// AppendLines records the hashes of lines an agent produced. Re-recording
// a line already present is a no-op, so an agent rewriting the same block
// contributes those lines once (NAV-52).
func (w *Writer) AppendLines(hashes []uint64, ts time.Time) error {
	if len(hashes) == 0 {
		return nil
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("journal: begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO agent_lines (repo_id, line_hash, first_ts) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("journal: prepare line insert: %w", err)
	}
	defer stmt.Close()

	for _, h := range hashes {
		// SQLite INTEGER is signed 64-bit; the hash is unsigned. The bit
		// pattern round-trips, which is all a lookup key needs.
		if _, err := stmt.Exec(w.repoID, int64(h), ts.UnixNano()); err != nil {
			return fmt.Errorf("journal: insert line hash: %w", err)
		}
	}
	return tx.Commit()
}

// ReadLineHashes returns every line hash recorded for a repository since
// the given time.
func ReadLineHashes(dataDir, repoID string, since time.Time) (map[uint64]struct{}, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return nil, nil
	}

	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT line_hash FROM agent_lines WHERE repo_id = ? AND first_ts >= ?`,
		repoID, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("journal: query line hashes: %w", err)
	}
	defer rows.Close()

	out := map[uint64]struct{}{}
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("journal: scan line hash: %w", err)
		}
		out[uint64(h)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal: line hash rows: %w", err)
	}
	return out, nil
}

// ReadRange reads one repository's entries with timestamps in [since, until).
// until may be zero to mean "no upper bound". Returns nil, not an error, if
// no journal database exists yet.
func ReadRange(dataDir, repoID string, since, until time.Time) ([]Entry, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return nil, nil
	}

	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome, model, branch, mcp_server, user_modified
	          FROM entries WHERE repo_id = ? AND ts >= ?`
	args := []any{repoID, since.UnixNano()}
	if !until.IsZero() {
		query += " AND ts < ?"
		args = append(args, until.UnixNano())
	}
	query += " ORDER BY ts ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("journal: query: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var ts int64

		// Through Null* types so a NULL column comes back empty/nil rather
		// than as a zero value indistinguishable from a measurement.
		var model, branch, mcpServer sql.NullString
		var userModified sql.NullBool

		if err := rows.Scan(&ts, &e.Agent, &e.AgentVersion, &e.Session, &e.Event,
			&e.Tool, &e.File, &e.LinesAdded, &e.LinesRemoved, &e.HunkHash, &e.SpecVersion,
			&e.Outcome, &model, &branch, &mcpServer, &userModified); err != nil {
			return nil, fmt.Errorf("journal: scan row: %w", err)
		}
		e.Timestamp = time.Unix(0, ts).UTC()
		e.Model, e.Branch, e.MCPServer = model.String, branch.String, mcpServer.String
		if userModified.Valid {
			v := userModified.Bool
			e.UserModified = &v
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal: rows: %w", err)
	}
	return entries, nil
}

// CountSince reports how many entries and distinct sessions a repository has
// recorded since a moment, without loading any of them.
//
// ReadRange answers the same question by deserialising every row and taking
// len(), which is right when the caller wants the entries and wasteful when
// it wants a number: `dun status` was materialising thousands of structs per
// repository to print two integers.
//
// A zero `since` counts everything, which is what a repository that has never
// published needs.
func CountSince(dataDir, repoID string, since time.Time) (entries, sessions int, err error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return 0, 0, nil
	}
	db, err := open(dataDir)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()

	err = db.QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT session) FROM entries
		 WHERE repo_id = ? AND ts >= ?`,
		repoID, since.UnixNano()).Scan(&entries, &sessions)
	if err != nil {
		return 0, 0, fmt.Errorf("journal: count: %w", err)
	}
	return entries, sessions, nil
}

// Purge deletes every entry for one repository, leaving other repositories
// untouched. `dun journal purge` means "forget what I did in this repo" —
// a global store must not turn that into forgetting everything.
func Purge(dataDir, repoID string) (int64, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return 0, nil
	}

	db, err := open(dataDir)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM entries WHERE repo_id = ?`, repoID)
	if err != nil {
		return 0, fmt.Errorf("journal: purge: %w", err)
	}

	// Line hashes are part of what was recorded about this repository, so
	// "forget what I did here" has to take them too — leaving them behind
	// would make purge a half-truth.
	if _, err := db.Exec(`DELETE FROM agent_lines WHERE repo_id = ?`, repoID); err != nil {
		return 0, fmt.Errorf("journal: purge line hashes: %w", err)
	}

	// Metadata carries the contributor identity, so it goes too — leaving
	// it would keep the most identifying part of what was recorded.
	if _, err := db.Exec(`DELETE FROM repo_metadata WHERE repo_id = ?`, repoID); err != nil {
		return 0, fmt.Errorf("journal: purge metadata: %w", err)
	}

	if _, err := db.Exec(`DELETE FROM sessions WHERE repo_id = ?`, repoID); err != nil {
		return 0, fmt.Errorf("journal: purge sessions: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // the delete succeeded; a driver that won't report a count is not an error
	}
	return n, nil
}

func open(dataDir string) (*sql.DB, error) {
	// Every path into the database goes through here, so this is where the
	// directory guarantee belongs — doing it only in NewWriter would leave
	// a directory created by hand, or by an older version, permissive.
	if err := config.EnsureDir(dataDir); err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}

	// Concurrent access is the normal configuration, not an edge case: one
	// journal file per machine is shared by the commit hook, the daemon's
	// five-second tick, and any `dun ingest` run by hand — across every
	// repository at once.
	//
	// Two pragmas make that safe, and their absence was a real bug rather
	// than a theoretical one. Under the default rollback journal a second
	// writer fails immediately with SQLITE_BUSY, and every caller here
	// treats a journal error as "record nothing and carry on" — so a commit
	// made during a daemon tick lost its attribution silently and was
	// stamped undetermined, which reads as "no AI was used".
	//
	//   journal_mode=WAL  readers no longer block on a writer, so the hook
	//                     can read line hashes while the daemon ingests.
	//   busy_timeout=5000 a writer waits its turn instead of failing.
	//                     Five seconds is well inside the hook's own two
	//                     second budget for the case that matters, and the
	//                     alternative is losing the write entirely.
	//
	// Reproduced by TestConcurrentWritersDoNotLoseEntries, which fails
	// against a connection opened without these.
	dsn := DBPath(dataDir) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("journal: open db: %w", err)
	}
	// Retried, because busy_timeout does not cover this one statement on a
	// database that does not exist yet.
	//
	// The pragma is applied per connection once the file is open, but two
	// processes creating the journal for the first time both run CREATE
	// TABLE against a file with no WAL yet, and the loser gets SQLITE_BUSY
	// with nothing to wait on. That is a narrow window — first commit after
	// install, daemon starting at the same moment — and it fails in the way
	// that costs most: NewWriter errors, the caller records nothing, and
	// the commit is stamped undetermined.
	//
	// Everything here is CREATE ... IF NOT EXISTS, so retrying is safe and
	// the loser simply finds the tables already made.
	if err := execWithRetry(db, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("journal: init schema: %w", err)
	}
	// Columns added after a journal already exists are not created by
	// CREATE TABLE IF NOT EXISTS, so they are added here. An error means
	// the column is already present, which is the normal case.
	for _, alter := range migrations {
		_, _ = db.Exec(alter)
	}

	// The schema exec is what actually creates the file, so the mode can
	// only be fixed after it.
	if err := tightenDBPerms(dataDir); err != nil {
		db.Close()
		return nil, fmt.Errorf("journal: %w", err)
	}
	return db, nil
}

// execWithRetry runs a statement, waiting out a lock instead of failing.
//
// Needed only where busy_timeout cannot help: the schema exec against a
// database file that does not exist yet, where there is no established lock
// to queue behind and SQLite returns SQLITE_BUSY immediately.
//
// Bounded and short. The point is to survive two processes starting in the
// same instant — a first commit while the daemon is launching — not to wait
// out a long transaction. Anything still locked after this returns the error
// and the caller degrades exactly as it did before.
//
// Safe to repeat: every statement in the schema is CREATE ... IF NOT EXISTS,
// so the loser of the race simply finds the tables already there.
func execWithRetry(db *sql.DB, stmt string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = db.Exec(stmt); err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "SQLITE_BUSY") &&
			!strings.Contains(err.Error(), "database is locked") {
			return err
		}
		time.Sleep(time.Duration(20*(attempt+1)) * time.Millisecond)
	}
	return err
}

// vacuumThreshold is how much free space must accumulate before Prune
// rewrites the file.
//
// Deleting rows moves pages to SQLite's freelist rather than shrinking the
// file, and VACUUM reclaims them by rewriting the whole database — which
// needs free disk equal to the file size and holds an exclusive lock. Doing
// that after every prune would rewrite megabytes to reclaim kilobytes on
// every run but the first, since freed pages are reused by later inserts.
const vacuumThreshold = 4 << 20

// Prune deletes line hashes older than the given time, across every
// repository.
//
// Global rather than repository-scoped, unlike Purge. Purge means "forget
// what I did in this repository" and must be scoped to be honest; retention
// is disk hygiene, and scoping it would leave the hashes of a repository
// nobody works in any more sitting there forever.
//
// Only agent_lines is touched. That table and its indexes are roughly 72% of
// the file and grow about seven rows per journal entry, so it is the whole
// of the growth problem. Entries, sessions and metadata are kept: the report
// builds its history from them, `dun verify` reports recency from them, and
// an old commit stays checkable against them long after its line hashes have
// gone (NAV-21).
//
// The caller decides the cutoff. Nothing here reads the configured retention
// window, because the safety of any cutoff depends on the caller having
// published the data first.
func Prune(dataDir string, olderThan time.Time) (deleted int64, vacuumed bool, err error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return 0, false, nil
	}

	db, err := open(dataDir)
	if err != nil {
		return 0, false, err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM agent_lines WHERE first_ts < ?`, olderThan.UnixNano())
	if err != nil {
		return 0, false, fmt.Errorf("journal: prune: %w", err)
	}
	deleted, _ = res.RowsAffected()
	if deleted == 0 {
		return 0, false, nil
	}

	// Rewrite only when there is something worth reclaiming. VACUUM cannot
	// run inside a transaction and is the one operation here that is slow
	// in proportion to the whole file rather than to what was deleted.
	var freePages, pageSize int64
	if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return deleted, false, nil // the delete stands; reclaiming is best-effort
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return deleted, false, nil
	}
	if freePages*pageSize < vacuumThreshold {
		return deleted, false, nil
	}

	if _, err := db.Exec(`VACUUM`); err != nil {
		// A failed vacuum leaves a correct database that is merely larger
		// than it needs to be, so it is not worth failing the prune over.
		return deleted, false, nil
	}
	return deleted, true, nil
}

// PruneCount reports how many line hashes a prune would delete, without
// deleting anything. For `dun journal prune --dry-run`.
func PruneCount(dataDir string, olderThan time.Time) (int64, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return 0, nil
	}
	db, err := open(dataDir)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var n int64
	err = db.QueryRow(`SELECT COUNT(*) FROM agent_lines WHERE first_ts < ?`,
		olderThan.UnixNano()).Scan(&n)
	return n, err
}
