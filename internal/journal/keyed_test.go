package journal

import (
	"testing"
	"time"
)

func keyedTablePresent(t *testing.T, dataDir string) bool {
	t.Helper()
	db, err := open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ok, err := keyedTableExists(db)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// Everything open-source dun does, including reading keyed lines, purging and
// pruning, must leave the journal without the keyed table.
func TestKeyedTableNeverCreatedByDefaultPaths(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines([]uint64{1, 2}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendKeyedLines(1, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	w.Close()

	if got, err := ReadKeyedLineHashes(dir, "repo", 1, time.Time{}); err != nil || got != nil {
		t.Fatalf("ReadKeyedLineHashes = %v, %v; want nil, nil", got, err)
	}
	if _, err := PruneCount(dir, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Prune(dir, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := Purge(dir, "other"); err != nil {
		t.Fatal(err)
	}
	if keyedTablePresent(t, dir) {
		t.Fatal("keyed table created without an opted-in write")
	}
}

func TestKeyedLinesRoundTripPurgeAndPrune(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now()

	w, err := NewWriter(dir, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendKeyedLines(0, []uint64{1}, recent); err == nil {
		t.Fatal("key version 0 accepted")
	}
	if err := w.AppendKeyedLines(1, []uint64{10, 11, 10}, old); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendKeyedLines(1, []uint64{12, ^uint64(0)}, recent); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendKeyedLines(2, []uint64{20}, recent); err != nil {
		t.Fatal(err)
	}
	w.Close()
	other, err := NewWriter(dir, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendKeyedLines(1, []uint64{99}, recent); err != nil {
		t.Fatal(err)
	}
	other.Close()

	v1, err := ReadKeyedLineHashes(dir, "repo", 1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []uint64{10, 11, 12, ^uint64(0)} {
		if _, ok := v1[h]; !ok {
			t.Errorf("v1 missing %d", h)
		}
	}
	if len(v1) != 4 {
		t.Errorf("v1 = %v, want 4 hashes (versions and repos kept apart)", v1)
	}
	if unkeyed, _ := ReadLineHashes(dir, "repo", time.Time{}); len(unkeyed) != 0 {
		t.Errorf("keyed writes leaked into agent_lines: %v", unkeyed)
	}

	cut := time.Now().Add(-24 * time.Hour)
	if n, err := PruneCount(dir, cut); err != nil || n != 2 {
		t.Fatalf("PruneCount = %d, %v; want 2", n, err)
	}
	if n, _, err := Prune(dir, cut); err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2", n, err)
	}
	if v1, _ = ReadKeyedLineHashes(dir, "repo", 1, time.Time{}); len(v1) != 2 {
		t.Fatalf("after prune v1 = %v, want 2", v1)
	}

	if _, err := Purge(dir, "repo"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{1, 2} {
		if got, _ := ReadKeyedLineHashes(dir, "repo", v, time.Time{}); len(got) != 0 {
			t.Errorf("purge left keyed v%d: %v", v, got)
		}
	}
	if got, _ := ReadKeyedLineHashes(dir, "other", 1, time.Time{}); len(got) != 1 {
		t.Errorf("purge of repo touched other: %v", got)
	}
}

// A journal written by a keyed caller is still a valid journal for a dun that
// knows nothing about keyed lines: the default read path is unaffected.
func TestKeyedTableDoesNotDisturbDefaultReads(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines([]uint64{5}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendKeyedLines(1, []uint64{6}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got, err := ReadLineHashes(dir, "repo", time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadLineHashes = %v, %v", got, err)
	}
}
