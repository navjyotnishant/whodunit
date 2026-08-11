package journal

import (
	"os"
	"testing"
	"time"
)

func TestWriteAndReadRange(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
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

	if info, err := os.Stat(dbPath(dir)); err != nil || info.Size() == 0 {
		t.Fatalf("journal.db not persisted to disk: err=%v", err)
	}

	got, err := ReadRange(dir, base, base.Add(24*time.Hour))
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

	all, err := ReadRange(dir, base, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange unbounded: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 entries unbounded, got %d", len(all))
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
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

	entries, err := ReadRange(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("re-appending the same entry 3x: want 1 stored entry, got %d", len(entries))
	}
}

func TestReadRangeOnMissingJournal(t *testing.T) {
	dir := t.TempDir()
	entries, err := ReadRange(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ReadRange on missing journal = %v, want nil error", err)
	}
	if entries != nil {
		t.Errorf("ReadRange on missing journal = %v, want nil slice", entries)
	}
}
