// Package journal implements the local-only observation log (SPEC v0.2),
// backed by an embedded SQLite database — single file, no server process,
// no CGO (pure-Go driver, so cross-compilation for release binaries stays
// simple).
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
	UNIQUE(session, ts, tool, file, hunk_hash)
);
CREATE INDEX IF NOT EXISTS idx_entries_ts ON entries(ts);
CREATE INDEX IF NOT EXISTS idx_entries_file ON entries(file);
`

// dbPath returns the SQLite file location for a journal rooted at dir.
func dbPath(dir string) string {
	return filepath.Join(dir, "journal.db")
}

// Writer appends entries to the local journal database.
type Writer struct {
	db *sql.DB
}

// NewWriter opens (creating if needed) the journal database under dir.
func NewWriter(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("journal: create dir: %w", err)
	}
	db, err := open(dir)
	if err != nil {
		return nil, err
	}
	return &Writer{db: db}, nil
}

// Close releases the underlying database handle.
func (w *Writer) Close() error {
	return w.db.Close()
}

// Append writes one entry to the journal. Re-appending an entry already
// present (same session, timestamp, tool, file, and hunk hash) is a no-op —
// ingest can be re-run safely without duplicating history.
func (w *Writer) Append(e Entry) error {
	if e.SpecVersion == "" {
		e.SpecVersion = SpecVersion
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	_, err := w.db.Exec(
		`INSERT OR IGNORE INTO entries (ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UnixNano(), e.Agent, e.AgentVersion, e.Session, e.Event,
		e.Tool, e.File, e.LinesAdded, e.LinesRemoved, e.HunkHash, e.SpecVersion,
	)
	if err != nil {
		return fmt.Errorf("journal: insert entry: %w", err)
	}
	return nil
}

// ReadRange reads all entries with timestamps in [since, until).
// until may be zero to mean "no upper bound". Returns an empty slice, not
// an error, if no journal database exists yet at dir.
func ReadRange(dir string, since, until time.Time) ([]Entry, error) {
	if _, err := os.Stat(dbPath(dir)); os.IsNotExist(err) {
		return nil, nil
	}

	db, err := open(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT ts, agent, agent_version, session, event, tool, file, lines_added, lines_removed, hunk_hash, spec_version
	          FROM entries WHERE ts >= ?`
	args := []any{since.UnixNano()}
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

func open(dir string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath(dir))
	if err != nil {
		return nil, fmt.Errorf("journal: open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("journal: init schema: %w", err)
	}
	return db, nil
}
