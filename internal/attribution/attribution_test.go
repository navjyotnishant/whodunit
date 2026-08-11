package attribution

import (
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

func TestDetermineUndeterminedWhenNoCoverage(t *testing.T) {
	now := time.Now()
	got := Determine(nil, []string{"main.go"}, nil, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined", got)
	}
}

func TestDetermineObservedWhenFileCovered(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", AgentVersion: "2.1.227", Session: "s1", Event: "tool_use", Tool: "Edit", File: "main.go", LinesAdded: 3, LinesRemoved: 1, HunkHash: "sha256:abc"},
	}
	got := Determine(entries, []string{"main.go"}, nil, now)
	if got.Status != spec.StatusAssisted || got.Method != spec.MethodObserved {
		t.Errorf("Determine() = %+v, want assisted/observed", got)
	}
	if got.Agent != "claude-code" || got.Session != "s1" {
		t.Errorf("Determine() metadata wrong: %+v", got)
	}
}

func TestDetermineIntersectedWhenHunkHashMatches(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go", HunkHash: "sha256:abc"},
	}
	staged := map[string]bool{"sha256:abc": true}
	got := Determine(entries, []string{"main.go"}, staged, now)
	if got.Method != spec.MethodIntersected {
		t.Errorf("Determine() method = %v, want intersected when hunk hash matches", got.Method)
	}
}

func TestDetermineStaysObservedWhenHunkHashMissing(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go", HunkHash: "sha256:abc"},
	}
	staged := map[string]bool{"sha256:different": true}
	got := Determine(entries, []string{"main.go"}, staged, now)
	if got.Method != spec.MethodObserved {
		t.Errorf("Determine() method = %v, want observed when hunk hash doesn't match", got.Method)
	}
}

func TestDetermineIgnoresOutOfWindowEntries(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-30 * 24 * time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 3},
	}
	got := Determine(entries, []string{"main.go"}, nil, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined for stale entry", got)
	}
}

func TestDetermineIgnoresUnrelatedFiles(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "other.go", LinesAdded: 3},
	}
	got := Determine(entries, []string{"main.go"}, nil, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined for unrelated file", got)
	}
}
