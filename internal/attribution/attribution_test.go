package attribution

import (
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// noCommitLines means the staged diff's line counts were unavailable, so a
// ratio cannot be computed.
var noCommitLines = CommitLines{}

func TestDetermineUndeterminedWhenNoCoverage(t *testing.T) {
	now := time.Now()
	got := Determine(nil, []string{"main.go"}, nil, noCommitLines, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined", got)
	}
}

func TestDetermineObservedWhenFileCovered(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", AgentVersion: "2.1.227", Session: "s1", Event: "tool_use", Tool: "Edit", File: "main.go", LinesAdded: 3, LinesRemoved: 1, HunkHash: "sha256:abc"},
	}
	got := Determine(entries, []string{"main.go"}, nil, noCommitLines, now)
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
	staged := map[string]int{"sha256:abc": 20}
	got := Determine(entries, []string{"main.go"}, staged, noCommitLines, now)
	if got.Method != spec.MethodIntersected {
		t.Errorf("Determine() method = %v, want intersected when hunk hash matches", got.Method)
	}
}

func TestDetermineStaysObservedWhenHunkHashMissing(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go", HunkHash: "sha256:abc"},
	}
	staged := map[string]int{"sha256:different": 20}
	got := Determine(entries, []string{"main.go"}, staged, noCommitLines, now)
	if got.Method != spec.MethodObserved {
		t.Errorf("Determine() method = %v, want observed when hunk hash doesn't match", got.Method)
	}
}

func TestDetermineIgnoresOutOfWindowEntries(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-30 * 24 * time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 3},
	}
	got := Determine(entries, []string{"main.go"}, nil, noCommitLines, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined for stale entry", got)
	}
}

func TestDetermineIgnoresUnrelatedFiles(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "other.go", LinesAdded: 3},
	}
	got := Determine(entries, []string{"main.go"}, nil, noCommitLines, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined for unrelated file", got)
	}
}

func TestDetermineRatioIsAgentsShareOfStagedLines(t *testing.T) {
	// Two staged hunks of 20 lines each; the agent produced one of them.
	// The commit changed 60+20=80 lines. 20/80 = 0.25.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", HunkHash: "sha256:mine"},
	}
	staged := map[string]int{"sha256:mine": 20, "sha256:theirs": 20}

	got := Determine(entries, []string{"main.go"}, staged, CommitLines{Added: 60, Removed: 20}, now)
	if got.Ratio == nil {
		t.Fatal("Ratio was not computed")
	}
	if *got.Ratio < 0.24 || *got.Ratio > 0.26 {
		t.Errorf("Ratio = %v, want 0.25 (20 agent lines of 80 changed)", *got.Ratio)
	}
}

func TestDetermineCountsRewrittenHunkOnce(t *testing.T) {
	// Regression for the finding that motivated deduplication: an agent
	// writes a block and rewrites it, producing several journal entries for
	// the same staged hunk. Summing them counted the same 30 staged lines
	// three times and drove the raw ratio above 4 on this project's own
	// history. The commit contains that hunk once, so it counts once.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-3 * time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 30, HunkHash: "sha256:same"},
		{Timestamp: now.Add(-2 * time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 30, HunkHash: "sha256:same"},
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 30, HunkHash: "sha256:same"},
	}
	staged := map[string]int{"sha256:same": 30}

	got := Determine(entries, []string{"main.go"}, staged, CommitLines{Added: 60}, now)
	if got.Ratio == nil {
		t.Fatal("Ratio was not computed")
	}
	if *got.Ratio < 0.49 || *got.Ratio > 0.51 {
		t.Errorf("Ratio = %v, want 0.5 — three rewrites of one 30-line hunk in a 60-line commit", *got.Ratio)
	}
}

func TestDetermineOmitsRatioWithoutCommitLines(t *testing.T) {
	// No denominator means no honest ratio. It must be absent, not zero:
	// 0.00 would assert the agent contributed nothing.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", HunkHash: "sha256:abc"},
	}
	staged := map[string]int{"sha256:abc": 30}
	got := Determine(entries, []string{"main.go"}, staged, noCommitLines, now)
	if got.Ratio != nil {
		t.Errorf("Ratio = %v, want nil when the commit's line counts are unknown", *got.Ratio)
	}
}

func TestDetermineOmitsRatioWhenNoHunkMatched(t *testing.T) {
	// method=observed means the agent touched the file but none of its text
	// survived into the staged diff. There is no share to report.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Event: "tool_use", File: "main.go", LinesAdded: 30, HunkHash: "sha256:gone"},
	}
	staged := map[string]int{"sha256:different": 30}

	got := Determine(entries, []string{"main.go"}, staged, CommitLines{Added: 60}, now)
	if got.Method != spec.MethodObserved {
		t.Errorf("Method = %v, want observed", got.Method)
	}
	if got.Ratio != nil {
		t.Errorf("Ratio = %v, want nil when no agent hunk reached the commit", *got.Ratio)
	}
}

func TestComputeRatioCountsDeletionInTheDenominator(t *testing.T) {
	// NAV-8 option B: the denominator is total changed lines, so a
	// deletion-heavy commit is not treated as a tiny change.
	r, ok := computeRatio(10, 0, 40)
	if !ok {
		t.Fatal("computeRatio reported nothing for a deletion-heavy change")
	}
	if r < 0.24 || r > 0.26 {
		t.Errorf("computeRatio = %v, want 0.25 (10 agent lines of 40 deleted)", r)
	}
}

func TestComputeRatioRejectsZeroDenominator(t *testing.T) {
	if _, ok := computeRatio(10, 0, 0); ok {
		t.Error("computeRatio reported a value with nothing to divide by")
	}
}

func TestParseNumstatSkipsBinaryFiles(t *testing.T) {
	// Binary files report "-" rather than counts. Treating them as zero
	// would be harmless here, but parsing them as anything else would
	// corrupt the denominator.
	out := "2\t1\tf.txt\n-\t-\tbin.dat\n10\t3\tother.go\n"
	added, removed := parseNumstat(out)
	if added != 12 || removed != 4 {
		t.Errorf("parseNumstat = (%d, %d), want (12, 4)", added, removed)
	}
}

func TestParseNumstatOnEmptyDiff(t *testing.T) {
	added, removed := parseNumstat("")
	if added != 0 || removed != 0 {
		t.Errorf("parseNumstat on empty = (%d, %d), want (0, 0)", added, removed)
	}
}

func TestComputeRatioOmitsSharesThatWouldRenderAsZero(t *testing.T) {
	// The trailer renders two decimals. A share below 0.005 would be
	// written "ratio=0.00", asserting the agent contributed nothing —
	// which is a different claim from "contributed a little".
	if _, ok := computeRatio(1, 365, 0); ok {
		t.Error("computeRatio reported a share that would render as 0.00")
	}
	// Just above the threshold still reports.
	if _, ok := computeRatio(1, 100, 0); !ok {
		t.Error("computeRatio omitted a share that renders as 0.01")
	}
}
