package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testRepo = "root-sha-aaa"

func TestWriteAndReadRange(t *testing.T) {
	home := t.TempDir()
	w, err := NewWriter(home, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: base, Agent: "claude-code", Session: "s1", Event: "tool_use", Tool: "Edit", File: "main.go", LinesAdded: 5},
		{Timestamp: base.Add(time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", Tool: "Write", File: "writer.go", LinesAdded: 10},
		{Timestamp: base.Add(48 * time.Hour), Agent: "claude-code", Session: "s2", Event: "tool_use", Tool: "Edit", File: "old.go", LinesAdded: 1},
	}
	for _, e := range entries {
		if err := w.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if info, err := os.Stat(DBPath(home)); err != nil || info.Size() == 0 {
		t.Fatalf("journal.db not persisted to disk: err=%v", err)
	}

	got, err := ReadRange(home, testRepo, base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries in range, got %d", len(got))
	}
	for _, e := range got {
		if e.SpecVersion != SpecVersion {
			t.Errorf("entry missing spec_version: %+v", e)
		}
	}

	all, err := ReadRange(home, testRepo, base, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange unbounded: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 entries unbounded, got %d", len(all))
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	home := t.TempDir()
	w, err := NewWriter(home, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	e := Entry{Timestamp: time.Now().UTC(), Agent: "claude-code", Session: "s1", Event: "tool_use", Tool: "Edit", File: "main.go", LinesAdded: 5}
	for i := 0; i < 3; i++ {
		if err := w.Append(e); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	entries, err := ReadRange(home, testRepo, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("re-appending the same entry 3x: want 1 stored entry, got %d", len(entries))
	}
}

func TestEntriesAreScopedByRepo(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()

	writeFor := func(repoID, file string) {
		t.Helper()
		w, err := NewWriter(home, repoID)
		if err != nil {
			t.Fatalf("NewWriter(%s): %v", repoID, err)
		}
		defer w.Close()
		if err := w.Append(Entry{Timestamp: now, Agent: "claude-code", Session: "s", Event: "tool_use", Tool: "Edit", File: file}); err != nil {
			t.Fatalf("Append(%s): %v", repoID, err)
		}
	}

	writeFor("repo-a", "a.go")
	writeFor("repo-b", "b.go")

	a, err := ReadRange(home, "repo-a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange(repo-a): %v", err)
	}
	if len(a) != 1 || a[0].File != "a.go" {
		t.Fatalf("repo-a should see only its own entry, got %+v", a)
	}

	b, err := ReadRange(home, "repo-b", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange(repo-b): %v", err)
	}
	if len(b) != 1 || b[0].File != "b.go" {
		t.Fatalf("repo-b should see only its own entry, got %+v", b)
	}
}

func TestSameEntryInTwoReposIsNotDeduped(t *testing.T) {
	// The uniqueness constraint includes repo_id: two repos legitimately
	// having an identical-looking event must both be recorded.
	home := t.TempDir()
	e := Entry{
		Timestamp: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		Agent:     "claude-code", Session: "shared", Event: "tool_use",
		Tool: "Edit", File: "main.go", HunkHash: "sha256:same",
	}

	for _, repo := range []string{"repo-a", "repo-b"} {
		w, err := NewWriter(home, repo)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
		w.Close()
	}

	for _, repo := range []string{"repo-a", "repo-b"} {
		got, err := ReadRange(home, repo, time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("ReadRange(%s): %v", repo, err)
		}
		if len(got) != 1 {
			t.Errorf("%s: want 1 entry, got %d", repo, len(got))
		}
	}
}

func TestPurgeOnlyAffectsOneRepo(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()

	for _, repo := range []string{"repo-a", "repo-b"} {
		w, err := NewWriter(home, repo)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.Append(Entry{Timestamp: now, Agent: "claude-code", Session: "s", Event: "tool_use", Tool: "Edit", File: repo + ".go"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		w.Close()
	}

	n, err := Purge(home, "repo-a")
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 1 {
		t.Errorf("Purge removed %d rows, want 1", n)
	}

	a, _ := ReadRange(home, "repo-a", time.Time{}, time.Time{})
	if len(a) != 0 {
		t.Errorf("repo-a should be empty after purge, got %d entries", len(a))
	}

	// The whole point of a per-repo purge in a shared store.
	b, _ := ReadRange(home, "repo-b", time.Time{}, time.Time{})
	if len(b) != 1 {
		t.Errorf("purging repo-a must not touch repo-b, but repo-b has %d entries", len(b))
	}
}

func TestReadRangeOnMissingJournal(t *testing.T) {
	entries, err := ReadRange(t.TempDir(), testRepo, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange on missing journal = %v, want nil error", err)
	}
	if entries != nil {
		t.Errorf("ReadRange on missing journal = %v, want nil slice", entries)
	}
}

func TestPurgeOnMissingJournal(t *testing.T) {
	n, err := Purge(t.TempDir(), testRepo)
	if err != nil {
		t.Fatalf("Purge on missing journal = %v, want nil error", err)
	}
	if n != 0 {
		t.Errorf("Purge on missing journal removed %d rows, want 0", n)
	}
}

func TestNewWriterRequiresRepoID(t *testing.T) {
	// An empty repo id would write rows no repo-scoped query could find.
	if _, err := NewWriter(t.TempDir(), ""); err == nil {
		t.Error("NewWriter with an empty repo id = nil error, want a refusal")
	}
}

func TestDatabaseIsNotWorldReadable(t *testing.T) {
	// The journal records which files were edited and when. The SQLite
	// driver creates the file 0644 by default, which is readable by every
	// user on a shared machine.
	dataDir := t.TempDir()
	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Append(Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "x.go"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	w.Close()

	info, err := os.Stat(DBPath(dataDir))
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal.db mode = %o, want 600 (owner only)", perm)
	}
}

func TestExistingWorldReadableDatabaseIsTightened(t *testing.T) {
	// A database created before this rule existed must be repaired, not
	// left permissive forever.
	dataDir := t.TempDir()
	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Close()

	if err := os.Chmod(DBPath(dataDir), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	w2, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter (reopen): %v", err)
	}
	w2.Close()

	info, err := os.Stat(DBPath(dataDir))
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal.db mode after reopen = %o, want 600", perm)
	}
}

func TestExistingPermissiveDataDirIsTightened(t *testing.T) {
	// A data directory created by hand (or by an older version) must be
	// repaired on the next open, not left readable by everyone.
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Close()

	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("data dir mode = %o, want 700 (owner only)", perm)
	}
}

func TestAppendAndReadLineHashes(t *testing.T) {
	dataDir := t.TempDir()
	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	now := time.Now().UTC()
	if err := w.AppendLines([]uint64{1, 2, 3}, now); err != nil {
		t.Fatalf("AppendLines: %v", err)
	}

	got, err := ReadLineHashes(dataDir, testRepo, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ReadLineHashes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 line hashes, got %d", len(got))
	}
}

func TestAppendLinesDeduplicates(t *testing.T) {
	// An agent rewriting the same block contributes those lines once —
	// this is what stops the rewrite inflation NAV-8 hit.
	dataDir := t.TempDir()
	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := w.AppendLines([]uint64{7, 8}, now); err != nil {
			t.Fatalf("AppendLines #%d: %v", i, err)
		}
	}

	got, err := ReadLineHashes(dataDir, testRepo, time.Time{})
	if err != nil {
		t.Fatalf("ReadLineHashes: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 distinct hashes after 3 identical appends, got %d", len(got))
	}
}

func TestLineHashesAreScopedByRepo(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC()

	for _, repo := range []string{"repo-a", "repo-b"} {
		w, err := NewWriter(dataDir, repo)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.AppendLines([]uint64{42}, now); err != nil {
			t.Fatalf("AppendLines: %v", err)
		}
		w.Close()
	}

	// The same hash in two repos is two rows; neither repo sees the other's.
	for _, repo := range []string{"repo-a", "repo-b"} {
		got, err := ReadLineHashes(dataDir, repo, time.Time{})
		if err != nil {
			t.Fatalf("ReadLineHashes(%s): %v", repo, err)
		}
		if len(got) != 1 {
			t.Errorf("%s: want 1 hash, got %d", repo, len(got))
		}
	}
}

func TestPurgeRemovesLineHashesToo(t *testing.T) {
	// "Forget what I did in this repo" has to take the line hashes as
	// well, or purge is a half-truth.
	dataDir := t.TempDir()
	now := time.Now().UTC()

	w, _ := NewWriter(dataDir, testRepo)
	w.Append(Entry{Timestamp: now, Agent: "claude-code", Event: "tool_use", File: "x.go"})
	w.AppendLines([]uint64{1, 2, 3}, now)
	w.Close()

	other, _ := NewWriter(dataDir, "other-repo")
	other.AppendLines([]uint64{9}, now)
	other.Close()

	if _, err := Purge(dataDir, testRepo); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	got, _ := ReadLineHashes(dataDir, testRepo, time.Time{})
	if len(got) != 0 {
		t.Errorf("purge left %d line hashes behind", len(got))
	}

	survived, _ := ReadLineHashes(dataDir, "other-repo", time.Time{})
	if len(survived) != 1 {
		t.Errorf("purge destroyed another repository's line hashes: %d survived, want 1", len(survived))
	}
}

func TestReadLineHashesOnMissingJournal(t *testing.T) {
	got, err := ReadLineHashes(t.TempDir(), testRepo, time.Time{})
	if err != nil {
		t.Fatalf("ReadLineHashes on missing journal = %v, want nil error", err)
	}
	if len(got) != 0 {
		t.Errorf("want no hashes, got %d", len(got))
	}
}

func TestAppendLinesWithNoHashesIsANoop(t *testing.T) {
	dataDir := t.TempDir()
	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	if err := w.AppendLines(nil, time.Now()); err != nil {
		t.Errorf("AppendLines(nil) = %v, want nil", err)
	}
}
