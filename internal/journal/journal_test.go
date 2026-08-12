package journal

import (
	"os"
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
