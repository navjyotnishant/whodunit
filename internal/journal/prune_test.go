// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Retention deletes line hashes and nothing else.

package journal

import (
	"os"
	"testing"
	"time"
)

func writeAged(t *testing.T, dir, repo string, ts time.Time, hashes []uint64) {
	t.Helper()
	w, err := NewWriter(dir, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(Entry{
		Timestamp: ts, Agent: "claude-code", Session: "s", Event: "tool_use",
		Tool: "Edit", File: "/f.go", HunkHash: "h" + ts.Format("150405.000000000"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines(hashes, ts); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, dir, table string) int {
	t.Helper()
	db, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneRemovesOldLineHashesOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	writeAged(t, dir, "r", now.Add(-90*24*time.Hour), []uint64{1, 2, 3})
	writeAged(t, dir, "r", now, []uint64{10, 11})

	entriesBefore := countRows(t, dir, "entries")

	deleted, _, err := Prune(dir, now.Add(-60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Errorf("deleted %d line hashes, want the 3 older than the cutoff", deleted)
	}

	// The recent hashes are what the hook still needs. Losing them would
	// downgrade an intersected commit to observed, silently.
	left, err := ReadLineHashes(dir, "r", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Errorf("%d hashes survived, want the 2 inside the window", len(left))
	}

	// The assertion that matters. Entries carry the report's history and
	// keep an old commit checkable after its line hashes are gone; a later
	// change that "helpfully" extends the delete to them fails here.
	if after := countRows(t, dir, "entries"); after != entriesBefore {
		t.Errorf("prune touched entries: %d before, %d after — retention is "+
			"line hashes only", entriesBefore, after)
	}
}

func TestPruneWithNothingOldEnoughDoesNothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeAged(t, dir, "r", now, []uint64{1, 2, 3})

	deleted, vacuumed, err := Prune(dir, now.Add(-60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted %d rows when nothing was old enough", deleted)
	}
	if vacuumed {
		t.Error("vacuumed despite deleting nothing — that rewrites the whole file for no gain")
	}
}

// A dry run has to report what a real one would delete, or the flag is
// worse than not having it.
func TestPruneCountMatchesWhatPruneDeletes(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeAged(t, dir, "r", now.Add(-90*24*time.Hour), []uint64{1, 2, 3, 4})
	writeAged(t, dir, "r", now, []uint64{10})

	cut := now.Add(-60 * 24 * time.Hour)
	predicted, err := PruneCount(dir, cut)
	if err != nil {
		t.Fatal(err)
	}
	deleted, _, err := Prune(dir, cut)
	if err != nil {
		t.Fatal(err)
	}
	if predicted != deleted {
		t.Errorf("--dry-run promised %d, prune deleted %d", predicted, deleted)
	}
}

func TestPruneOnAnAbsentJournalIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Prune(dir, time.Now()); err != nil {
		t.Errorf("pruning a journal that does not exist errored: %v", err)
	}
	if _, err := os.Stat(DBPath(dir)); !os.IsNotExist(err) {
		t.Error("prune created a journal that was not there")
	}
}
