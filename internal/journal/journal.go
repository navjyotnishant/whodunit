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
	UNIQUE(repo_id, session, ts, tool, file, hunk_hash)
);
CREATE INDEX IF NOT EXISTS idx_entries_repo_ts ON entries(repo_id, ts);
CREATE INDEX IF NOT EXISTS idx_entries_repo_file ON entries(repo_id, file);
`

// DBPath returns the journal database location inside the given whodunit
// home directory.
func DBPath(home string) string {
	return filepath.Join(home, "journal.db")
}

// Writer appends entries for one repository to the global journal.
type Writer struct {
	db     *sql.DB
	repoID string
}

// NewWriter opens (creating if needed) the global journal database under
// home, scoped to repoID. Every entry written through it belongs to that
// repository; nothing can write across repos by accident.
func NewWriter(home, repoID string) (*Writer, error) {
	if repoID == "" {
		return nil, fmt.Errorf("journal: repo id is required")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("journal: create home: %w", err)
	}
	db, err := open(home)
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
		`INSERT OR IGNORE INTO entries (repo_id, ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.repoID, e.Timestamp.UnixNano(), e.Agent, e.AgentVersion, e.Session, e.Event,
		e.Tool, e.File, e.LinesAdded, e.LinesRemoved, e.HunkHash, e.SpecVersion,
	)
	if err != nil {
		return fmt.Errorf("journal: insert entry: %w", err)
	}
	return nil
}

// ReadRange reads one repository's entries with timestamps in [since, until).
// until may be zero to mean "no upper bound". Returns nil, not an error, if
// no journal database exists yet.
func ReadRange(home, repoID string, since, until time.Time) ([]Entry, error) {
	if _, err := os.Stat(DBPath(home)); os.IsNotExist(err) {
		return nil, nil
	}

	db, err := open(home)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version
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
			&e.Tool, &e.File, &e.LinesAdded, &e.LinesRemoved, &e.HunkHash, &e.SpecVersion); err != nil {
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
func Purge(home, repoID string) (int64, error) {
	if _, err := os.Stat(DBPath(home)); os.IsNotExist(err) {
		return 0, nil
	}

	db, err := open(home)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM entries WHERE repo_id = ?`, repoID)
	if err != nil {
		return 0, fmt.Errorf("journal: purge: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // the delete succeeded; a driver that won't report a count is not an error
	}
	return n, nil
}

func open(home string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", DBPath(home))
	if err != nil {
		return nil, fmt.Errorf("journal: open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("journal: init schema: %w", err)
	}
	return db, nil
}
