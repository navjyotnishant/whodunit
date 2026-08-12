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

// Snapshot is one immutable pre-adoption measurement of a repository.
type Snapshot struct {
	SchemaVersion string    `json:"schema_version"`
	CapturedAt    time.Time `json:"captured_at"`
	WindowDays    int       `json:"window_days"`
	HeadSHA       string    `json:"head_sha"`

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

// Capture computes a snapshot over the last windowDays of history.
func Capture(windowDays int, manual *ManualMetrics, now time.Time) (Snapshot, error) {
	snap := Snapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    now.UTC(),
		WindowDays:    windowDays,
		Manual:        manual,
		Git:           GitMetrics{PurposeDistribution: map[purpose.Purpose]int{}},
	}

	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve HEAD (is this an empty repo?): %w", err)
	}
	snap.HeadSHA = strings.TrimSpace(string(head))

	since := now.AddDate(0, 0, -windowDays).Format(time.RFC3339)
	commits, err := collectCommits(since)
	if err != nil {
		return Snapshot{}, err
	}

	snap.Git.Commits = len(commits)
	if len(commits) == 0 {
		return snap, nil
	}

	weeks := float64(windowDays) / 7
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

func collectCommits(sinceRFC3339 string) ([]commitRecord, error) {
	const sep = "\x1f"
	out, err := exec.Command("git", "log", "--since="+sinceRFC3339,
		"--format=%H"+sep+"%aI"+sep+"%s", "--shortstat").Output()
	if err != nil {
		return nil, fmt.Errorf("read git log: %w", err)
	}

	filesBySHA, err := commitFiles(sinceRFC3339)
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

func commitFiles(sinceRFC3339 string) (map[string][]string, error) {
	out, err := exec.Command("git", "log", "--since="+sinceRFC3339,
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
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing baseline at %s: a baseline is immutable, and the window it measured cannot be recaptured", path)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
