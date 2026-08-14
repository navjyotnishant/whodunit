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
		`INSERT INTO entries (repo_id, ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repo_id, session, ts, tool, file, hunk_hash) DO UPDATE SET
		   outcome=excluded.outcome,
		   lines_added=excluded.lines_added,
		   lines_removed=excluded.lines_removed`,
		w.repoID, e.Timestamp.UnixNano(), e.Agent, e.AgentVersion, e.Session, e.Event,
		e.Tool, e.File, e.LinesAdded, e.LinesRemoved, e.HunkHash, e.SpecVersion, e.Outcome,
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
}

// UpsertSession records or updates one session's activity. A session grows
// as work continues, so a later ingest overwrites the earlier counts rather
// than adding to them.
func (w *Writer) UpsertSession(s Session) error {
	if s.Session == "" {
		return nil
	}
	_, err := w.db.Exec(
		`INSERT INTO sessions (repo_id, session, agent, agent_version, first_seen, last_seen,
		   user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repo_id, session) DO UPDATE SET
		   agent_version=excluded.agent_version, last_seen=excluded.last_seen,
		   user_messages=excluded.user_messages, agent_messages=excluded.agent_messages,
		   tool_calls=excluded.tool_calls, distinct_tools=excluded.distinct_tools,
		   mcp_calls=excluded.mcp_calls`,
		w.repoID, s.Session, s.Agent, s.AgentVersion,
		s.FirstSeen.UnixNano(), s.LastSeen.UnixNano(),
		s.UserMessages, s.AgentMessages, s.ToolCalls, s.DistinctTools, s.MCPCalls)
	if err != nil {
		return fmt.Errorf("journal: upsert session: %w", err)
	}
	return nil
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
		        user_messages, agent_messages, tool_calls, distinct_tools, mcp_calls
		 FROM sessions WHERE repo_id = ? ORDER BY first_seen`, repoID)
	if err != nil {
		return nil, fmt.Errorf("journal: query sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		var first, last int64
		if err := rows.Scan(&s.Session, &s.Agent, &s.AgentVersion, &first, &last,
			&s.UserMessages, &s.AgentMessages, &s.ToolCalls, &s.DistinctTools, &s.MCPCalls); err != nil {
			return nil, fmt.Errorf("journal: scan session: %w", err)
		}
		s.FirstSeen = time.Unix(0, first).UTC()
		s.LastSeen = time.Unix(0, last).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
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

	query := `SELECT ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version, outcome
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
		if err := rows.Scan(&ts, &e.Agent, &e.AgentVersion, &e.Session, &e.Event,
			&e.Tool, &e.File, &e.LinesAdded, &e.LinesRemoved, &e.HunkHash, &e.SpecVersion,
			&e.Outcome); err != nil {
			return nil, fmt.Errorf("journal: scan row: %w", err)
		}
		e.Timestamp = time.Unix(0, ts).UTC()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal: rows: %w", err)
	}
	return entries, nil
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

	db, err := sql.Open("sqlite", DBPath(dataDir))
	if err != nil {
		return nil, fmt.Errorf("journal: open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
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
