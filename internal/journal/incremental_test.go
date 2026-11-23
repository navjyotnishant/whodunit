package journal

import (
	"testing"
	"time"
)

// Incremental sync reads rows by when they were written or changed, so a
// re-ingest that changes nothing must not make old rows look new, and one
// that does change a row must.
func TestChangedSinceTracksRealChangesOnly(t *testing.T) {
	home := t.TempDir()
	w, err := NewWriter(home, testRepo)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// An event that happened long ago, ingested now: it must count as new.
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := Entry{Timestamp: old, Agent: "claude-code", Session: "s1", Event: "tool_use", Tool: "Edit", File: "a.go"}
	s := Session{Session: "s1", Agent: "claude-code", FirstSeen: old, LastSeen: old, ToolCalls: 1}
	if err := w.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertSession(s); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines([]uint64{42}, old); err != nil {
		t.Fatal(err)
	}

	mark := time.Now()
	time.Sleep(2 * time.Millisecond)

	// Same rows again, as a lookback re-ingest would write them.
	if err := w.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertSession(s); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines([]uint64{42}, old); err != nil {
		t.Fatal(err)
	}
	assertChanged(t, home, mark, 0, 0, 0)

	// The event gains an outcome and the session grows: both go out.
	e.Outcome = "accepted"
	s.ToolCalls = 2
	if err := w.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertSession(s); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendLines([]uint64{43}, old); err != nil {
		t.Fatal(err)
	}
	assertChanged(t, home, mark, 1, 1, 1)

	// Zero since is a full read.
	assertChanged(t, home, time.Time{}, 1, 1, 2)
}

func assertChanged(t *testing.T, home string, since time.Time, entries, sessions, lines int) {
	t.Helper()
	es, err := ReadChangedSince(home, testRepo, since)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := ReadSessionsChangedSince(home, testRepo, since)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := ReadLineHashesAddedSince(home, testRepo, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != entries || len(ss) != sessions || len(ls) != lines {
		t.Fatalf("changed since %v: got %d entries, %d sessions, %d lines; want %d, %d, %d",
			since, len(es), len(ss), len(ls), entries, sessions, lines)
	}
}

func TestSyncMarkIsPerTarget(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	if got, err := SyncMark(home, testRepo, "mysql://a/lake"); err != nil || !got.IsZero() {
		t.Fatalf("unset mark: got %v, %v", got, err)
	}
	if err := SetSyncMark(home, testRepo, "mysql://a/lake", at); err != nil {
		t.Fatal(err)
	}
	if got, _ := SyncMark(home, testRepo, "mysql://a/lake"); !got.Equal(at) {
		t.Fatalf("mark: got %v, want %v", got, at)
	}
	if got, _ := SyncMark(home, testRepo, "mysql://b/lake"); !got.IsZero() {
		t.Fatalf("another target must start from nothing, got %v", got)
	}
}

// A row an older dun wrote has no change time. The next incremental sync
// must still send it, rather than skip it forever.
func TestUnmarkedRowsJoinTheNextIncrementalSync(t *testing.T) {
	home := t.TempDir()
	w, err := NewWriter(home, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Entry{Timestamp: time.Now(), Agent: "claude-code", Session: "s1",
		Event: "tool_use", Tool: "Edit", File: "a.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Exec(`UPDATE entries SET changed_at = 0`); err != nil {
		t.Fatal(err)
	}
	w.Close()

	since := time.Now()
	assertChanged(t, home, since, 0, 0, 0)
	if err := StampUnmarked(home, testRepo, since); err != nil {
		t.Fatal(err)
	}
	assertChanged(t, home, since, 1, 0, 0)
}
