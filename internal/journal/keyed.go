// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Opt-in journal table for keyed line hashes (ADR 0008 in
// whodunit-pro). Created on first write, never by open.

package journal

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

// keyedLinesTable holds keyed line hashes (linehash.OfKeyed) beside the
// unkeyed agent_lines, never instead of them.
//
// It is deliberately absent from schema and migrations. open never creates
// it, so a journal used only by open-source dun keeps exactly today's
// schema, and the compatibility suite fails if that changes. The table is
// created by the first AppendKeyedLines, which only an opted-in caller makes.
//
// key_version is part of the key: during a rotation the same line is
// recorded under the old and the new key, and a reader asks for one.
const keyedLinesTable = "agent_lines_keyed"

const keyedLinesSchema = `
CREATE TABLE IF NOT EXISTS agent_lines_keyed (
	repo_id     TEXT    NOT NULL,
	key_version INTEGER NOT NULL,
	line_hash   INTEGER NOT NULL,
	first_ts    INTEGER NOT NULL,
	PRIMARY KEY (repo_id, key_version, line_hash)
);
CREATE INDEX IF NOT EXISTS idx_agent_lines_keyed_repo_ts ON agent_lines_keyed(repo_id, key_version, first_ts);
`

// AppendKeyedLines records keyed hashes of lines an agent produced, creating
// the keyed table on first use. Re-recording a line is a no-op, as in
// AppendLines.
func (w *Writer) AppendKeyedLines(keyVersion int, hashes []uint64, ts time.Time) error {
	if len(hashes) == 0 {
		return nil
	}
	if keyVersion < 1 {
		return fmt.Errorf("journal: key version must be at least 1, got %d", keyVersion)
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	if err := execWithRetry(w.db, keyedLinesSchema); err != nil {
		return fmt.Errorf("journal: create keyed lines table: %w", err)
	}

	return insertHashes(w.db, `INSERT OR IGNORE INTO agent_lines_keyed (repo_id, key_version, line_hash, first_ts) VALUES (?, ?, ?, ?)`,
		hashes, func(h int64) []any { return []any{w.repoID, keyVersion, h, ts.UnixNano()} })
}

// ReadKeyedLineHashes returns the keyed hashes recorded for a repository
// under one key version since the given time. A journal that never had
// keyed lines yields nil, and reading never creates the table.
func ReadKeyedLineHashes(dataDir, repoID string, keyVersion int, since time.Time) (map[uint64]struct{}, error) {
	if _, err := os.Stat(DBPath(dataDir)); os.IsNotExist(err) {
		return nil, nil
	}
	db, err := open(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	exists, err := keyedTableExists(db)
	if err != nil || !exists {
		return nil, err
	}
	return queryHashes(db,
		`SELECT line_hash FROM agent_lines_keyed WHERE repo_id = ? AND key_version = ? AND first_ts >= ?`,
		repoID, keyVersion, since.UnixNano())
}

// keyedTableExists reports whether an opted-in caller ever wrote keyed lines.
// Purge and prune use it so they cover keyed rows without creating the table.
func keyedTableExists(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, keyedLinesTable).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("journal: check keyed lines table: %w", err)
	}
	return n > 0, nil
}
