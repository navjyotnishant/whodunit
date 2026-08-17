// Package baseline captures a dated, immutable snapshot of a repository's
// delivery metrics BEFORE whodunit's hooks are installed (NAV-14).
//
// This is the one task in the plan that gets impossible rather than late:
// once hooks land, the pre-adoption window closes permanently, and a
// before/after comparison (NAV-46) has nothing to compare against.
//
// Only git-derivable metrics are computed. PR throughput and change-failure
// rate need GitHub/CI data this tool deliberately never fetches (no network
// calls, ever), so they are optional manual-entry fields the user supplies —
// recorded as provided, never inferred.
package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/purpose"
)

// SchemaVersion identifies the snapshot format. A baseline is compared
// against future data months later, so the definition it was computed
// under has to travel with it.
const SchemaVersion = "1"

// Window is the span of history a snapshot measures.
//
// A window anchored only to "the last N days" ends at the moment of capture,
// which stops being pre-adoption the day hooks are installed — a late capture
// then compares AI-assisted work against itself. Explicit bounds let the user
// name the period they remember working without an agent, and make the
// capture reproducible rather than dependent on the day it happened to run.
type Window struct {
	Since time.Time
	Until time.Time
}

// WindowFromDays builds the legacy "last N days" window, ending now.
func WindowFromDays(days int, now time.Time) Window {
	return Window{Since: now.AddDate(0, 0, -days), Until: now}
}

// Days is the window's length, rounded to whole days. Kept on the snapshot
// so everything downstream that reads window_days keeps working.
func (w Window) Days() int {
	return int(w.Until.Sub(w.Since).Hours() / 24)
}

// Snapshot is one immutable pre-adoption measurement of a repository.
type Snapshot struct {
	SchemaVersion string    `json:"schema_version"`
	CapturedAt    time.Time `json:"captured_at"`
	WindowDays    int       `json:"window_days"`
	HeadSHA       string    `json:"head_sha"`

	// The measured span. Older snapshots predate these fields and carry only
	// WindowDays, so both stay populated.
	WindowSince time.Time `json:"window_since"`
	WindowUntil time.Time `json:"window_until"`

	// Git-derived, computed automatically.
	Git GitMetrics `json:"git"`

	// Manually supplied — this tool cannot see PR or CI systems. Omitted
	// entirely when not provided rather than recorded as zero, so a missing
	// measurement is never mistaken for a measured zero.
	Manual *ManualMetrics `json:"manual,omitempty"`
}

// GitMetrics are the metrics computable from git history alone.
type GitMetrics struct {
	Commits             int                     `json:"commits"`
	CommitsPerWeek      float64                 `json:"commits_per_week"`
	MedianDiffLines     int                     `json:"median_diff_lines"`
	MeanHoursBetween    float64                 `json:"mean_hours_between_commits"`
	Reverts             int                     `json:"reverts"`
	RevertRate          float64                 `json:"revert_rate"`
	PurposeDistribution map[purpose.Purpose]int `json:"purpose_distribution"`
}

// ManualMetrics carry numbers a human read off GitHub Insights, a CI
// dashboard, or an internal tool. Pointers so "not supplied" stays
// distinguishable from "measured as zero".
type ManualMetrics struct {
	PRsMerged          *int     `json:"prs_merged,omitempty"`
	MedianCycleTimeHrs *float64 `json:"median_cycle_time_hours,omitempty"`
	ChangeFailureRate  *float64 `json:"change_failure_rate,omitempty"`
	Note               string   `json:"note,omitempty"`
}

// Capture computes a snapshot over the last windowDays of history, ending now.
func Capture(windowDays int, manual *ManualMetrics, now time.Time) (Snapshot, error) {
	return CaptureWindow(WindowFromDays(windowDays, now), manual, now)
}

// CaptureWindow computes a snapshot over an explicit span of history.
func CaptureWindow(w Window, manual *ManualMetrics, now time.Time) (Snapshot, error) {
	if !w.Until.After(w.Since) {
		return Snapshot{}, fmt.Errorf("window ends before it starts: --since %s is not before --until %s",
			w.Since.Format("2006-01-02"), w.Until.Format("2006-01-02"))
	}

	snap := Snapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    now.UTC(),
		WindowDays:    w.Days(),
		WindowSince:   w.Since.UTC(),
		WindowUntil:   w.Until.UTC(),
		Manual:        manual,
		Git:           GitMetrics{PurposeDistribution: map[purpose.Purpose]int{}},
	}

	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve HEAD (is this an empty repo?): %w", err)
	}
	snap.HeadSHA = strings.TrimSpace(string(head))

	since := w.Since.Format(time.RFC3339)
	until := w.Until.Format(time.RFC3339)
	commits, err := collectCommits(since, until)
	if err != nil {
		return Snapshot{}, err
	}

	snap.Git.Commits = len(commits)
	if len(commits) == 0 {
		return snap, nil
	}

	weeks := float64(w.Days()) / 7
	if weeks > 0 {
		snap.Git.CommitsPerWeek = float64(len(commits)) / weeks
	}

	var diffSizes []int
	var timestamps []time.Time
	for _, c := range commits {
		diffSizes = append(diffSizes, c.diffLines)
		timestamps = append(timestamps, c.ts)
		snap.Git.PurposeDistribution[c.purpose]++
		if c.isRevert {
			snap.Git.Reverts++
		}
	}

	snap.Git.MedianDiffLines = median(diffSizes)
	snap.Git.RevertRate = float64(snap.Git.Reverts) / float64(len(commits))
	snap.Git.MeanHoursBetween = meanGapHours(timestamps)

	return snap, nil
}

type commitRecord struct {
	ts        time.Time
	subject   string
	diffLines int
	purpose   purpose.Purpose
	isRevert  bool
}

func collectCommits(sinceRFC3339, untilRFC3339 string) ([]commitRecord, error) {
	const sep = "\x1f"
	out, err := exec.Command("git", "log", "--since="+sinceRFC3339, "--until="+untilRFC3339,
		"--format=%H"+sep+"%aI"+sep+"%s", "--shortstat").Output()
	if err != nil {
		return nil, fmt.Errorf("read git log: %w", err)
	}

	filesBySHA, err := commitFiles(sinceRFC3339, untilRFC3339)
	if err != nil {
		return nil, err
	}

	var records []commitRecord
	var current *commitRecord
	var currentSHA string

	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, sep) {
			if current != nil {
				current.purpose = purpose.Classify(current.subject, filesBySHA[currentSHA])
				records = append(records, *current)
			}
			parts := strings.SplitN(line, sep, 3)
			if len(parts) != 3 {
				current = nil
				continue
			}
			ts, _ := time.Parse(time.RFC3339, parts[1])
			currentSHA = parts[0]
			current = &commitRecord{
				ts:       ts,
				subject:  parts[2],
				isRevert: isRevertSubject(parts[2]),
			}
			continue
		}
		// " 3 files changed, 41 insertions(+), 7 deletions(-)"
		if current != nil && strings.Contains(line, "changed") {
			current.diffLines = parseShortstatLines(line)
		}
	}
	if current != nil {
		current.purpose = purpose.Classify(current.subject, filesBySHA[currentSHA])
		records = append(records, *current)
	}

	return records, nil
}

func commitFiles(sinceRFC3339, untilRFC3339 string) (map[string][]string, error) {
	out, err := exec.Command("git", "log", "--since="+sinceRFC3339, "--until="+untilRFC3339,
		"--format=COMMIT %H", "--name-only").Output()
	if err != nil {
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

// isRevertSubject matches both git's own revert convention and the
// Conventional Commits revert type.
func isRevertSubject(subject string) bool {
	lower := strings.ToLower(subject)
	return strings.HasPrefix(lower, `revert "`) || strings.HasPrefix(lower, "revert:")
}

// parseShortstatLines sums insertions and deletions from a --shortstat line.
func parseShortstatLines(line string) int {
	total := 0
	for _, field := range strings.Split(line, ",") {
		field = strings.TrimSpace(field)
		if !strings.Contains(field, "insertion") && !strings.Contains(field, "deletion") {
			continue
		}
		numStr := strings.Fields(field)
		if len(numStr) == 0 {
			continue
		}
		if n, err := strconv.Atoi(numStr[0]); err == nil {
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

// meanGapHours is the average wall-clock gap between consecutive commits —
// a rough proxy for delivery cadence, NOT true cycle time (which needs
// PR open/merge timestamps this tool can't see). Named and documented as
// the proxy it is so it isn't later mistaken for DORA cycle time.
func meanGapHours(timestamps []time.Time) float64 {
	if len(timestamps) < 2 {
		return 0
	}
	sorted := make([]time.Time, len(timestamps))
	copy(sorted, timestamps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	total := sorted[len(sorted)-1].Sub(sorted[0])
	return total.Hours() / float64(len(sorted)-1)
}

// Load reads a snapshot written by Write. Returns (nil, nil) when no
// baseline exists at path — a repo that never captured one is a normal
// state to report, not an error to fail on.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse baseline at %s: %w", path, err)
	}
	if snap.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("baseline at %s uses schema version %q, this build understands %q — "+
			"comparing across schema versions would silently compare different definitions",
			path, snap.SchemaVersion, SchemaVersion)
	}
	return &snap, nil
}

// Write saves the snapshot as indented JSON. It refuses to overwrite an
// existing file: a baseline is immutable by definition, and silently
// replacing one destroys the only copy of a window that cannot be recaptured.
func Write(path string, snap Snapshot) error {
	return write(path, snap, false)
}

// WriteForce replaces an existing snapshot. Immutability protects a good
// baseline from being destroyed, but it equally blocks fixing a wrong one —
// a capture made with the wrong window is otherwise only removable by hand.
// The caller is expected to show what is being replaced first.
func WriteForce(path string, snap Snapshot) error {
	return write(path, snap, true)
}

func write(path string, snap Snapshot, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("refusing to overwrite existing baseline at %s: a baseline is immutable, and the window it measured cannot be recaptured (pass --force to replace it)", path)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	// Owner-only, like everything else under ~/.whodunit.
	//
	// This wrote 0644 while the directory around it is 0700 and the README
	// promises "0700 directories, 0600 files". The directory made it
	// unreadable to others in practice, so the loose mode never showed —
	// but a snapshot names a repository and reports its commit cadence and
	// revert rate, which is nobody else's business on a shared machine, and
	// a file that is only protected by its parent stays protected only
	// until it is copied somewhere else.
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
