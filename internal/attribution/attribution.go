// Package attribution determines the highest-confidence AI-Attribution
// trailer for a commit, by looking up staged changes against the local
// journal.
package attribution

import (
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// LookbackWindow bounds how far back a journal entry can be and still count
// as covering the change about to be committed.
//
// Thirty days rather than the seven it started at. Seven covered a working
// week and lost anything that sat over a holiday or a long-running branch,
// which is exactly the work most likely to be agent-heavy. Measured against a
// synthetic year of data the wider window costs about 26ms per commit — 2% of
// the hook's budget — because the query is indexed and scales linearly.
//
// Exported because journal retention is derived from it: pruning line hashes
// the hook would still have matched turns an intersected commit into an
// observed one, silently. Two constants that must stay in a ratio are one
// constant and a multiplier, or they drift the first time someone changes
// one.
const LookbackWindow = 30 * 24 * time.Hour

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

// comparablePath spells a path one way so two producers can be compared.
//
// Separators only, and no filesystem access: this runs per staged file on
// the commit path. The larger differences — /tmp against /private/tmp, and
// Windows' 8.3 short names — are resolved by the callers, which know the
// repository root and can do it once.
func comparablePath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

func Determine(entries []journal.Entry, stagedFiles []string, agentLineHashes map[uint64]struct{}, staged StagedEvidence, now time.Time) spec.Trailer {
	// Keyed on a separator-neutral spelling.
	//
	// The two sides are produced by different code: git yields the staged
	// list, an agent's transcript yields e.File. On Windows those disagree
	// on the separator — "C:\repo\main.go" against "C:/repo/main.go" — and
	// an exact string match then finds nothing, so every commit is stamped
	// undetermined. That reads as "no AI was used" rather than "the two
	// halves spelled the path differently" (NAV-21).
	stagedSet := map[string]bool{}
	for _, f := range stagedFiles {
		stagedSet[comparablePath(f)] = true
	}

	since := now.Add(-LookbackWindow)
	var relevant []journal.Entry
	for _, e := range entries {
		if e.Event != "tool_use" || e.Timestamp.Before(since) {
			continue
		}
		if stagedSet[comparablePath(e.File)] {
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
