// Package attribution determines the highest-confidence AI-Attribution
// trailer for a commit, by looking up staged changes against the local
// journal.
package attribution

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// lookbackWindow bounds how far back a journal entry can be and still count
// as covering the change about to be committed. Generous, since edits often
// sit staged for a while before commit.
const lookbackWindow = 7 * 24 * time.Hour

// Determine picks a trailer for a commit whose staged files are stagedFiles,
// by checking whether any recent journal entry touched one of those files.
// When stagedHunkHashes is non-empty and at least one journal entry's hash
// appears in it, confidence upgrades from observed (same file touched) to
// intersected (the exact text the agent produced is what got staged) —
// NAV-26/NAV-27's highest-confidence method. Pass a nil/empty map to skip
// hunk matching and always get observed at best (e.g. when git diff isn't
// available). Returns method=undetermined if the journal has no relevant
// coverage — per NAV-21, absence must never be silently upgraded.
// CommitLines carries the staged diff's own line counts, the denominator
// for ratio. Zero values mean "unknown", and ratio is then omitted rather
// than guessed.
type CommitLines struct {
	Added   int
	Removed int
}

// StagedEvidence is what the staged diff itself contributes to a
// determination: the lines it adds, hashed per line, plus its total line
// counts.
type StagedEvidence struct {
	// Lines are the hashes of substantive added lines, in diff order.
	Lines []uint64

	// Commit counts every changed line, the ratio's denominator.
	Commit CommitLines
}

func Determine(entries []journal.Entry, stagedFiles []string, agentLineHashes map[uint64]struct{}, staged StagedEvidence, now time.Time) spec.Trailer {
	stagedSet := map[string]bool{}
	for _, f := range stagedFiles {
		stagedSet[f] = true
	}

	since := now.Add(-lookbackWindow)
	var relevant []journal.Entry
	for _, e := range entries {
		if e.Event != "tool_use" || e.Timestamp.Before(since) {
			continue
		}
		if stagedSet[e.File] {
			relevant = append(relevant, e)
		}
	}

	if len(relevant) == 0 {
		return spec.Undetermined()
	}

	agent, version, session := relevant[0].Agent, relevant[0].AgentVersion, relevant[0].Session

	// Count staged lines that match a line the agent produced. Distinct
	// hashes only: a file that legitimately repeats a line should not let
	// one agent-written line claim several.
	//
	// This is the intersected signal too. Line-level overlap is what the
	// method was always meant to mean — matching whole tool outputs against
	// whole diff hunks only ever fired when a file was created whole and
	// committed untouched (NAV-52).
	counted := map[uint64]bool{}
	agentLines := 0
	for _, h := range staged.Lines {
		if counted[h] {
			continue
		}
		if _, ok := agentLineHashes[h]; ok {
			counted[h] = true
			agentLines++
		}
	}

	method := spec.MethodObserved
	if agentLines > 0 {
		method = spec.MethodIntersected
	}

	trailer := spec.Trailer{
		Status:  spec.StatusAssisted,
		Method:  method,
		Agent:   agent,
		Version: version,
		Session: session,
		Extra:   map[string]string{},
	}

	if r, ok := computeRatio(agentLines, staged.Commit.Added, staged.Commit.Removed); ok {
		trailer.Ratio = &r
	}

	return trailer
}
