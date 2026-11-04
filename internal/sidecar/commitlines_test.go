package sidecar

import (
	"testing"
	"time"
)

func commitLinesTablePresent(t *testing.T, db *Store) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='whodunit_commit_lines'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// Default sync, with no commit lines in the payload, must not create the
// opt-in table, and must report exactly what it reported before.
func TestWriteWithoutCommitLinesCreatesNoTable(t *testing.T) {
	db := openStore(t)
	counts, err := Write(db, samplePayload(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if commitLinesTablePresent(t, db) {
		t.Fatal("whodunit_commit_lines created by a default sync")
	}
	if counts.CommitLines != 0 || counts.Commits != 1 || counts.Lines != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestWriteCommitLinesIsIdempotentAndJoins(t *testing.T) {
	db := openStore(t)
	now := time.Now().UTC()
	p := samplePayload(now)
	p.CommitLines = []CommitLineRow{
		{RepoID: "repo", CommitSHA: "abc", FilePath: "main.go", Hash: 42, SyncedAt: now},
		{RepoID: "repo", CommitSHA: "abc", FilePath: "main.go", Hash: ^uint64(0), SyncedAt: now},
	}
	for i := 0; i < 2; i++ {
		counts, err := Write(db, p)
		if err != nil {
			t.Fatalf("write #%d: %v", i, err)
		}
		if counts.CommitLines != 2 {
			t.Fatalf("write #%d: CommitLines = %d", i, counts.CommitLines)
		}
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM whodunit_commit_lines`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d after two writes, want 2", rows)
	}

	// The point of the table: which committed lines an agent wrote.
	var path string
	err := db.QueryRow(`SELECT c.file_path FROM whodunit_commit_lines c
		JOIN whodunit_event_lines e ON e.repo_id = c.repo_id AND e.line_hash = c.line_hash
		WHERE c.commit_sha = 'abc'`).Scan(&path)
	if err != nil || path != "main.go" {
		t.Fatalf("join = %q, %v", path, err)
	}

	var h int64
	if err := db.QueryRow(`SELECT line_hash FROM whodunit_commit_lines WHERE line_hash < 0`).Scan(&h); err != nil || uint64(h) != ^uint64(0) {
		t.Fatalf("high-bit hash did not round-trip: %d, %v", h, err)
	}
}

func TestUpsertCommitLineBothDialects(t *testing.T) {
	for _, mysql := range []bool{false, true} {
		q := upsertCommitLine(mysql)
		if want := 5; countPlaceholders(q) != want {
			t.Errorf("mysql=%v: %d placeholders, want %d", mysql, countPlaceholders(q), want)
		}
	}
}

func countPlaceholders(q string) int {
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
		}
	}
	return n
}
