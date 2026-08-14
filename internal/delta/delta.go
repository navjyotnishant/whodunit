// Package delta compares delivery metrics before and after AI-attribution
// adoption (NAV-46).
//
// Two independent cuts are produced, because either alone is misleading:
//
//   - Cross-period: the NAV-14 baseline window against a recent window.
//     Shows change over time, but attributes EVERY difference to adoption —
//     team size, deadlines, refactor sprints and holidays move the same
//     numbers. It is a correlation, never a cause, and is labeled as such.
//
//   - Within-period: assisted commits against undetermined commits inside
//     the SAME window. Controls for calendar effects the cross-period cut
//     cannot, at the cost of comparing self-selected groups of work.
//
// Revert rate travels with every velocity figure. A throughput gain that
// arrives with more reverts is deferred rework, not speed, and reporting
// the first without the second would overstate the result.
package delta

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Metrics is one measured group — a time window, or a slice of commits
// within a window.
type Metrics struct {
	Commits         int                     `json:"commits"`
	CommitsPerWeek  float64                 `json:"commits_per_week"`
	MedianDiffLines int                     `json:"median_diff_lines"`
	Reverts         int                     `json:"reverts"`
	RevertRate      float64                 `json:"revert_rate"`
	Purposes        map[purpose.Purpose]int `json:"purposes"`
}

// CrossPeriod compares the pre-adoption baseline against a recent window.
type CrossPeriod struct {
	Baseline           Metrics  `json:"baseline"`
	Current            Metrics  `json:"current"`
	BaselineCapturedAt string   `json:"baseline_captured_at"`
	Confounders        []string `json:"confounders"`
}

// WithinPeriod compares assisted against undetermined commits in the same
// window — the cut that controls for calendar effects.
type WithinPeriod struct {
	Assisted     Metrics `json:"assisted"`
	Undetermined Metrics `json:"undetermined"`
	WindowDays   int     `json:"window_days"`
}

// Result carries both cuts plus whatever caveats apply to this particular
// dataset, so a reader never sees a figure without the reason to doubt it.
type Result struct {
	Cross    *CrossPeriod `json:"cross_period,omitempty"`
	Within   WithinPeriod `json:"within_period"`
	Warnings []string     `json:"warnings,omitempty"`
}

// minCommitsForConfidence is the floor below which a rate computed from
// this data says more about noise than about the team. Chosen to be
// obviously-too-small rather than statistically derived — the point is to
// flag thin data, not to certify thick data.
const minCommitsForConfidence = 20

// standardConfounders are the alternative explanations that apply to any
// cross-period comparison of a real repository. Listed every time, because
// a reader who isn't told to consider them generally won't.
var standardConfounders = []string{
	"team size or composition changed between the two windows",
	"the type of work changed (a refactor sprint moves diff size and commit volume independently of tooling)",
	"calendar effects — holidays, deadlines, on-call rotations, vacations",
	"commit habits changed (squashing, smaller commits) without the underlying work changing",
	"adoption was not instantaneous; the current window may mix assisted and unassisted work",
}

// Compute builds both cuts. base may be nil when no baseline was ever
// captured — the within-period cut still works, and the caller is told the
// cross-period comparison is unavailable rather than shown a fabricated one.
func Compute(base *baseline.Snapshot, windowDays int, now time.Time) (Result, error) {
	commits, err := commitsSince(now.AddDate(0, 0, -windowDays))
	if err != nil {
		return Result{}, err
	}

	var assisted, undetermined []commitRecord
	for _, c := range commits {
		if c.assisted {
			assisted = append(assisted, c)
		} else {
			undetermined = append(undetermined, c)
		}
	}

	res := Result{
		Within: WithinPeriod{
			WindowDays:   windowDays,
			Assisted:     metricsFor(assisted, windowDays),
			Undetermined: metricsFor(undetermined, windowDays),
		},
	}

	if len(assisted) < minCommitsForConfidence || len(undetermined) < minCommitsForConfidence {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"thin data: %d assisted and %d undetermined commits in this window (under %d in either group). "+
				"Rates computed from this few commits move a lot on one or two commits.",
			len(assisted), len(undetermined), minCommitsForConfidence))
	}

	if base == nil {
		res.Warnings = append(res.Warnings,
			"no pre-adoption baseline found, so no before/after comparison is possible. "+
				"That window closed when hooks were installed and cannot be recaptured.")
		return res, nil
	}

	res.Cross = &CrossPeriod{
		BaselineCapturedAt: base.CapturedAt.Format(time.RFC3339),
		Baseline: Metrics{
			Commits:         base.Git.Commits,
			CommitsPerWeek:  base.Git.CommitsPerWeek,
			MedianDiffLines: base.Git.MedianDiffLines,
			Reverts:         base.Git.Reverts,
			RevertRate:      base.Git.RevertRate,
			Purposes:        base.Git.PurposeDistribution,
		},
		Current:     metricsFor(commits, windowDays),
		Confounders: standardConfounders,
	}

	if base.WindowDays != windowDays {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"window mismatch: the baseline measured %d days, this comparison uses %d. "+
				"Per-week rates are still comparable; raw counts are not.",
			base.WindowDays, windowDays))
	}

	return res, nil
}

// PercentChange returns the change from before to after as a fraction, and
// whether it is meaningful at all — a change from zero is undefined, not
// infinite, and must not be rendered as a number.
func PercentChange(before, after float64) (float64, bool) {
	if before == 0 {
		return 0, false
	}
	return (after - before) / before, true
}

type commitRecord struct {
	ts        time.Time
	subject   string
	diffLines int
	purpose   purpose.Purpose
	isRevert  bool
	assisted  bool
}

func metricsFor(commits []commitRecord, windowDays int) Metrics {
	m := Metrics{Purposes: map[purpose.Purpose]int{}, Commits: len(commits)}
	if len(commits) == 0 {
		return m
	}

	var sizes []int
	for _, c := range commits {
		sizes = append(sizes, c.diffLines)
		m.Purposes[c.purpose]++
		if c.isRevert {
			m.Reverts++
		}
	}

	if weeks := float64(windowDays) / 7; weeks > 0 {
		m.CommitsPerWeek = float64(len(commits)) / weeks
	}
	m.MedianDiffLines = median(sizes)
	m.RevertRate = float64(m.Reverts) / float64(len(commits))
	return m
}

// commitsSince reads commits in the window. The full message (%B) spans
// multiple lines and the AI-Attribution trailer sits at its END, so the
// record is delimited by an explicit separator rather than parsed
// line-by-line — a line-oriented parse silently sees only the first body
// line and reports every assisted commit as undetermined.
func commitsSince(since time.Time) ([]commitRecord, error) {
	const fieldSep = "\x1f"
	const recordSep = "\x1e"

	format := "%H" + fieldSep + "%aI" + fieldSep + "%B" + recordSep
	out, err := exec.Command("git", "log", "--since="+since.Format(time.RFC3339),
		"--format="+format).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return nil, nil
		}
		return nil, fmt.Errorf("read git log: %w", err)
	}

	filesBySHA, err := commitFiles(since)
	if err != nil {
		return nil, err
	}
	sizesBySHA, err := commitDiffSizes(since)
	if err != nil {
		return nil, err
	}

	var records []commitRecord
	for _, record := range strings.Split(string(out), recordSep) {
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}
		sha, dateStr, msg := parts[0], parts[1], strings.TrimRight(parts[2], "\n")
		subject := strings.SplitN(msg, "\n", 2)[0]
		ts, _ := time.Parse(time.RFC3339, dateStr)

		records = append(records, commitRecord{
			ts:        ts,
			subject:   subject,
			diffLines: sizesBySHA[sha],
			purpose:   purpose.Classify(msg, filesBySHA[sha]),
			isRevert:  isRevertSubject(subject),
			assisted:  isAssisted(msg),
		})
	}

	return records, nil
}

// commitDiffSizes maps each commit to its insertions+deletions. Kept as a
// separate pass for the same reason as commitFiles: --shortstat output
// interleaves awkwardly with a custom --format.
func commitDiffSizes(since time.Time) (map[string]int, error) {
	out, err := exec.Command("git", "log", "--since="+since.Format(time.RFC3339),
		"--format=COMMIT %H", "--shortstat").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("read git log --shortstat: %w", err)
	}

	result := map[string]int{}
	var sha string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "COMMIT "):
			sha = strings.TrimPrefix(line, "COMMIT ")
		case sha != "" && strings.Contains(line, "changed"):
			result[sha] = parseShortstat(line)
		}
	}
	return result, nil
}

// isAssisted reports whether a commit carries a valid AI-Attribution
// trailer with status=assisted. A missing or malformed trailer is
// undetermined, never treated as "no AI involvement" (NAV-21).
func isAssisted(commitMsg string) bool {
	prefix := spec.TrailerKey + ":"
	for _, line := range strings.Split(commitMsg, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		t, err := spec.Parse(strings.TrimSpace(line[len(prefix):]))
		if err != nil {
			continue
		}
		return t.Status == spec.StatusAssisted
	}
	return false
}

func commitFiles(since time.Time) (map[string][]string, error) {
	out, err := exec.Command("git", "log", "--since="+since.Format(time.RFC3339),
		"--format=COMMIT %H", "--name-only").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("read git log --name-only: %w", err)
	}
	result := map[string][]string{}
	var sha string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "COMMIT "):
			sha = strings.TrimPrefix(line, "COMMIT ")
		case strings.TrimSpace(line) != "" && sha != "":
			result[sha] = append(result[sha], line)
		}
	}
	return result, nil
}

func isRevertSubject(subject string) bool {
	lower := strings.ToLower(subject)
	return strings.HasPrefix(lower, `revert "`) || strings.HasPrefix(lower, "revert:")
}

func parseShortstat(line string) int {
	total := 0
	for _, field := range strings.Split(line, ",") {
		field = strings.TrimSpace(field)
		if !strings.Contains(field, "insertion") && !strings.Contains(field, "deletion") {
			continue
		}
		fields := strings.Fields(field)
		if len(fields) == 0 {
			continue
		}
		if n, err := strconv.Atoi(fields[0]); err == nil {
			total += n
		}
	}
	return total
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int, len(values))
	copy(sorted, values)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}
