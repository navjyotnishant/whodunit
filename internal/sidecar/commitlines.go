// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Opt-in table of the line hashes each commit added (ADR 0008
// in whodunit-pro). Created only when a payload carries such rows.

package sidecar

import (
	"fmt"
	"time"
)

// CommitLineRow is one row of whodunit_commit_lines: a distinct substantive
// line a commit added, with the file it is in.
type CommitLineRow struct {
	RepoID    string
	CommitSHA string
	FilePath  string // repo-relative, slash separated
	Hash      uint64 // linehash.Of, the scheme whodunit_event_lines uses
	SyncedAt  time.Time
}

// CommitLinesSchema is deliberately not part of Schema, Migrations or
// Indexes. EnsureSchema never creates it, so a DevLake database synced only
// by default configuration keeps exactly today's tables, and the
// compatibility suite fails if that changes. WriteProgress applies it when a
// payload carries commit lines, which happens only with sync.commit_lines on.
//
// Joined with whodunit_event_lines on (repo_id, line_hash), it answers what
// the trailer's ratio cannot: which committed lines, in which files, an
// agent wrote. That join is also what makes the table sensitive: it holds
// file paths per commit, which is why it is opt-in.
//
// The key needs no file column: line_hash already covers the path, so one
// line in one file of one commit is one row. Paths stay out of the key
// because MySQL caps a key at 3072 bytes.
const CommitLinesSchema = `
CREATE TABLE IF NOT EXISTS whodunit_commit_lines (
	repo_id    VARCHAR(64)  NOT NULL,
	commit_sha VARCHAR(64)  NOT NULL,
	line_hash  BIGINT       NOT NULL,
	file_path  VARCHAR(512) NOT NULL,
	synced_at  BIGINT       NOT NULL,
	PRIMARY KEY (repo_id, commit_sha, line_hash)
);
`

// commitLinesIndex serves the join with whodunit_event_lines.
const commitLinesIndex = `CREATE INDEX idx_whodunit_commit_lines_hash ON whodunit_commit_lines (repo_id, line_hash)`

// ensureCommitLinesSchema creates the opt-in table and its index. As in
// EnsureSchema, an index failure is ignored: the realistic cause is that it
// already exists, and MySQL has no CREATE INDEX IF NOT EXISTS.
func ensureCommitLinesSchema(db *Store) error {
	for _, stmt := range splitStatements(CommitLinesSchema) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply commit lines schema: %w", err)
		}
	}
	_, _ = db.Exec(commitLinesIndex)
	return nil
}

// The two dialects' upserts are constants, not built per call, so the SQL
// sent is fixed text and every value goes through a placeholder.
const (
	upsertCommitLineSQLite = `INSERT INTO whodunit_commit_lines (repo_id, commit_sha, line_hash, file_path, synced_at) VALUES (?, ?, ?, ?, ?)` +
		` ON CONFLICT(repo_id, commit_sha, line_hash) DO UPDATE SET file_path=excluded.file_path, synced_at=excluded.synced_at`
	upsertCommitLineMySQL = `INSERT INTO whodunit_commit_lines (repo_id, commit_sha, line_hash, file_path, synced_at) VALUES (?, ?, ?, ?, ?)` +
		` ON DUPLICATE KEY UPDATE file_path=VALUES(file_path), synced_at=VALUES(synced_at)`
)

func upsertCommitLine(mysql bool) string {
	if mysql {
		return upsertCommitLineMySQL
	}
	return upsertCommitLineSQLite
}
