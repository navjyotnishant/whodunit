package attribution

import (
	"os/exec"
	"strconv"
	"strings"
)

// StagedLineCounts returns the total added and removed lines across the
// staged diff — the denominator for `ratio` (NAV-8: total changed lines,
// not added-only).
//
// Binary files report "-" instead of counts and are skipped: they have no
// meaningful line count, and treating them as zero would quietly shrink the
// denominator and inflate the ratio.
func StagedLineCounts() (added, removed int, err error) {
	out, err := exec.Command("git", "diff", "--cached", "--numstat").Output()
	if err != nil {
		return 0, 0, err
	}
	a, r := parseNumstat(string(out))
	return a, r, nil
}

func parseNumstat(out string) (added, removed int) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		a, errA := strconv.Atoi(fields[0])
		r, errR := strconv.Atoi(fields[1])
		if errA != nil || errR != nil {
			continue // binary file ("-\t-\tpath"), or a line we do not understand
		}
		added += a
		removed += r
	}
	return added, removed
}

// computeRatio expresses the agent's share of a commit: staged lines that
// came from the agent, over all lines the commit changed. Additions and
// deletions both count in the denominator (NAV-8, option B — total changed
// matches how a diff reads, "+40 −12" being a 52-line change).
//
// agentLines must be measured in STAGED lines, deduplicated per hunk. An
// earlier version summed journal line counts instead, which counted every
// rewrite of the same block: on this project's own history that produced a
// raw ratio of 4.28, since an agent writes a file and rewrites it while
// the commit holds only the final state.
//
// What this number is still NOT: proof of authorship. It says the exact
// text of these staged hunks matches text an agent produced. A line the
// agent wrote and the developer then edited no longer matches, and counts
// as the developer's.
//
// Returns ok=false when there is nothing to divide by, so callers omit the
// field rather than emitting a fabricated 0.
func computeRatio(agentLines, commitAdded, commitRemoved int) (float64, bool) {
	denominator := commitAdded + commitRemoved
	if denominator <= 0 {
		return 0, false
	}
	if agentLines <= 0 {
		return 0, false
	}

	ratio := float64(agentLines) / float64(denominator)
	// Deduplicated staged lines cannot exceed the commit's own line count,
	// so this should be unreachable. Clamp anyway rather than emit a share
	// above 1 if some future counting change breaks that invariant.
	if ratio > 1 {
		ratio = 1
	}
	// The trailer renders two decimals, so anything under 0.005 would be
	// written as "ratio=0.00" — a claim that the agent contributed nothing,
	// which is not what a small positive share means. Omit instead.
	if ratio < 0.005 {
		return 0, false
	}
	return ratio, true
}
