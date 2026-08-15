package attribution

import (
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// noCommitLines means the staged diff's line counts were unavailable, so a
// ratio cannot be computed.
var noStagedEvidence = StagedEvidence{}

func TestDetermineUndeterminedWhenNoCoverage(t *testing.T) {
	now := time.Now()
	got := Determine(nil, []string{"main.go"}, nil, noStagedEvidence, now)
	if got.Status != spec.StatusUndetermined {
		t.Errorf("Determine() = %+v, want undetermined", got)
	}
}

func TestDetermineObservedWhenFileCovered(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", AgentVersion: "2.1.227", Session: "s1", Event: "tool_use", Tool: "Edit", File: "main.go", LinesAdded: 3, LinesRemoved: 1, HunkHash: "sha256:abc"},
	}
	got := Determine(entries, []string{"main.go"}, nil, noStagedEvidence, now)
	if got.Status != spec.StatusAssisted || got.Method != spec.MethodObserved {
		t.Errorf("Determine() = %+v, want assisted/observed", got)
	}
	if got.Agent != "claude-code" || got.Session != "s1" {
		t.Errorf("Determine() metadata wrong: %+v", got)
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

// lineSet builds the agent-line lookup the way the journal supplies it.
func lineSet(hashes ...uint64) map[uint64]struct{} {
	s := map[uint64]struct{}{}
	for _, h := range hashes {
		s[h] = struct{}{}
	}
	return s
}

func TestDetermineIntersectedWhenALineMatches(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	h := linehash.Of("main.go", "doWork()")

	got := Determine(entries, []string{"main.go"}, lineSet(h),
		StagedEvidence{Lines: []uint64{h}, Commit: CommitLines{Added: 10}}, now)

	if got.Method != spec.MethodIntersected {
		t.Errorf("Method = %v, want intersected when a staged line matches an agent line", got.Method)
	}
}

func TestDetermineStaysObservedWhenNoLineMatches(t *testing.T) {
	// The agent touched the file, but nothing it wrote survived into the
	// staged diff — the developer rewrote it.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	agentLine := linehash.Of("main.go", "agentWrote()")
	stagedLine := linehash.Of("main.go", "developerWrote()")

	got := Determine(entries, []string{"main.go"}, lineSet(agentLine),
		StagedEvidence{Lines: []uint64{stagedLine}, Commit: CommitLines{Added: 10}}, now)

	if got.Method != spec.MethodObserved {
		t.Errorf("Method = %v, want observed when no agent line survived", got.Method)
	}
	if got.Ratio != nil {
		t.Errorf("Ratio = %v, want nil when no agent line reached the commit", *got.Ratio)
	}
}

func TestDetermineRatioIsShareOfStagedLines(t *testing.T) {
	// Four staged lines, two of them the agent's; the commit changed
	// 6 added + 2 removed = 8 lines. 2/8 = 0.25.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	mine1 := linehash.Of("main.go", "agentLineOne()")
	mine2 := linehash.Of("main.go", "agentLineTwo()")
	theirs1 := linehash.Of("main.go", "humanLineOne()")
	theirs2 := linehash.Of("main.go", "humanLineTwo()")

	got := Determine(entries, []string{"main.go"}, lineSet(mine1, mine2),
		StagedEvidence{
			Lines:  []uint64{mine1, mine2, theirs1, theirs2},
			Commit: CommitLines{Added: 6, Removed: 2},
		}, now)

	if got.Ratio == nil {
		t.Fatal("Ratio was not computed")
	}
	if *got.Ratio < 0.24 || *got.Ratio > 0.26 {
		t.Errorf("Ratio = %v, want 0.25 (2 agent lines of 8 changed)", *got.Ratio)
	}
}

func TestDetermineSurvivesPartialEditing(t *testing.T) {
	// The case whole-output hashing could not handle (NAV-52): the agent
	// wrote three lines, the developer rewrote one, and the other two
	// still count.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	kept1 := linehash.Of("main.go", "setupThing()")
	kept2 := linehash.Of("main.go", "runThing()")
	replaced := linehash.Of("main.go", "agentVersionOfLine()")
	rewritten := linehash.Of("main.go", "humanVersionOfLine()")

	got := Determine(entries, []string{"main.go"}, lineSet(kept1, kept2, replaced),
		StagedEvidence{
			Lines:  []uint64{kept1, kept2, rewritten},
			Commit: CommitLines{Added: 3},
		}, now)

	if got.Method != spec.MethodIntersected {
		t.Errorf("Method = %v, want intersected", got.Method)
	}
	if got.Ratio == nil {
		t.Fatal("Ratio was not computed")
	}
	if *got.Ratio < 0.66 || *got.Ratio > 0.67 {
		t.Errorf("Ratio = %v, want ~0.67 (2 of 3 agent lines survived)", *got.Ratio)
	}
}

func TestDetermineCountsARepeatedLineOnce(t *testing.T) {
	// A file may legitimately repeat a line. One agent-written line must
	// not claim several staged occurrences.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	repeated := linehash.Of("main.go", "wg.Done()")

	got := Determine(entries, []string{"main.go"}, lineSet(repeated),
		StagedEvidence{
			Lines:  []uint64{repeated, repeated, repeated, repeated},
			Commit: CommitLines{Added: 4},
		}, now)

	if got.Ratio == nil {
		t.Fatal("Ratio was not computed")
	}
	if *got.Ratio < 0.24 || *got.Ratio > 0.26 {
		t.Errorf("Ratio = %v, want 0.25 — one distinct agent line of 4 changed", *got.Ratio)
	}
}

func TestDetermineOmitsRatioWithoutCommitLines(t *testing.T) {
	// No denominator means no honest ratio. It must be absent, not zero:
	// 0.00 would assert the agent contributed nothing.
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now.Add(-time.Hour), Agent: "claude-code", Session: "s1", Event: "tool_use", File: "main.go"},
	}
	h := linehash.Of("main.go", "doWork()")

	got := Determine(entries, []string{"main.go"}, lineSet(h),
		StagedEvidence{Lines: []uint64{h}}, now)

	if got.Ratio != nil {
		t.Errorf("Ratio = %v, want nil when the commit's line counts are unknown", *got.Ratio)
	}
}

func TestDetermineMatchesAcrossSeparatorSpellings(t *testing.T) {
	// git yields the staged list; an agent's transcript yields the entry's
	// File. On Windows those disagree on the separator, and an exact string
	// match then finds nothing — so a commit an agent demonstrably wrote is
	// stamped undetermined, which reads as "no AI was used".
	now := time.Now()
	entries := []journal.Entry{{
		Timestamp: now.Add(-time.Hour),
		Event:     "tool_use",
		Agent:     "claude-code",
		Session:   "s1",
		File:      "C:/repo/main.go",
	}}

	got := Determine(
		entries,
		[]string{`C:\repo\main.go`}, // the same file, spelled the other way
		nil,
		StagedEvidence{},
		now,
	)

	if got.Status != spec.StatusAssisted {
		t.Errorf("status = %q, want assisted: the transcript and the staged "+
			"list name the same file in different spellings", got.Status)
	}
	if got.Agent != "claude-code" {
		t.Errorf("agent = %q, want claude-code", got.Agent)
	}
}

// The model comes from the LAST relevant entry, not the first (NAV-117).
//
// A commit can contain edits from more than one model — a session that
// escalated part-way through, or two sessions touching the same files.
// First-seen would describe work that may since have been rewritten; the
// turn that finished the work is the one worth attributing, which is also
// how journal.Session resolves it.
func TestModelComesFromTheLastEntry(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	entries := []journal.Entry{
		{Event: "tool_use", Timestamp: base, File: "/repo/a.go",
			Agent: "claude-code", Model: "claude-haiku-4-5"},
		{Event: "tool_use", Timestamp: base.Add(time.Minute), File: "/repo/a.go",
			Agent: "claude-code", Model: "claude-opus-5"},
	}

	tr := Determine(entries, []string{"/repo/a.go"}, nil, StagedEvidence{}, time.Now())

	if tr.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want claude-opus-5 — the escalated-to model is "+
			"what finished the work", tr.Model)
	}
}

// An entry with no model does not blank one an earlier entry recorded.
// The scan walks backwards to the most recent entry that actually has
// one, rather than taking the last entry unconditionally.
func TestALaterEntryWithoutAModelDoesNotEraseIt(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	entries := []journal.Entry{
		{Event: "tool_use", Timestamp: base, File: "/repo/a.go",
			Agent: "claude-code", Model: "claude-opus-5"},
		{Event: "tool_use", Timestamp: base.Add(time.Minute), File: "/repo/a.go",
			Agent: "claude-code"}, // agy-shaped: no model on this entry
	}

	tr := Determine(entries, []string{"/repo/a.go"}, nil, StagedEvidence{}, time.Now())

	if tr.Model != "claude-opus-5" {
		t.Errorf("Model = %q; a later entry with no model erased one that was "+
			"recorded", tr.Model)
	}
}

// No model anywhere leaves the field empty, so the key is omitted from
// the trailer rather than written as unknown (NAV-21).
func TestNoModelLeavesTheFieldEmpty(t *testing.T) {
	entries := []journal.Entry{
		{Event: "tool_use", Timestamp: time.Now().UTC().Add(-time.Hour),
			File: "/repo/a.go", Agent: "agy"},
	}

	tr := Determine(entries, []string{"/repo/a.go"}, nil, StagedEvidence{}, time.Now())

	if tr.Model != "" {
		t.Errorf("Model = %q for entries that recorded none", tr.Model)
	}
	if strings.Contains(tr.Format(), "model") {
		t.Errorf("trailer mentions a model it does not have: %s", tr.Format())
	}
}
