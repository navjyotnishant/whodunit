// Package report computes coverage/method-mix/cost stats from git history
// and renders them into a single self-contained HTML file — no server, no
// CDN, no network call, so it works offline and on an intranet.
package report

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Commit is one examined commit with its trailer (if any) and classified
// purpose, in commit order (newest first, matching git log).
type Commit struct {
	SHA       string
	Timestamp time.Time
	Subject   string
	Files     []string
	Trailer   *spec.Trailer // nil if no valid trailer was found
	Purpose   purpose.Purpose

	// LinesAdded and LinesRemoved are the commit's own diff size. Binary
	// files contribute nothing rather than zero, since git reports no line
	// count for them.
	LinesAdded   int
	LinesRemoved int
}

// Stats is the aggregate computed from git history for one report run.
type Stats struct {
	Commits      []Commit
	TotalCommits int
	Covered      int // commits with a valid trailer (any status)
	Assisted     int // commits with status=assisted (used in the trailer, not the commit)
	MethodCount  map[spec.Method]int
	PurposeCount map[purpose.Purpose]int

	// HasBaseline says whether a pre-adoption snapshot exists for this
	// repository. Set by the caller: this package reads git and the
	// journal, and finding a baseline means knowing the whodunit home,
	// which is the command layer's business.
	HasBaseline bool
}

// Coverage returns the fraction of commits carrying a valid trailer.
func (s Stats) Coverage() float64 {
	if s.TotalCommits == 0 {
		return 0
	}
	return float64(s.Covered) / float64(s.TotalCommits)
}

// Penetration returns the fraction of *covered* commits that are assisted.
// Denominator excludes undetermined commits deliberately (NAV-40): reporting
// penetration over all commits understates it, and without coverage alongside
// it overstates confidence — Coverage() must always be shown next to this.
func (s Stats) Penetration() float64 {
	if s.Covered == 0 {
		return 0
	}
	return float64(s.Assisted) / float64(s.Covered)
}

const recordSep = "\x1e" // ASCII record separator: between commit metadata records
const fieldSep = "\x1f"  // ASCII unit separator: between fields within a commit

// Collect walks up to `limit` commits of git history and computes Stats.
//
// Metadata (sha/date/body) and file lists are fetched in two separate git
// invocations, keyed by sha, rather than one combined --name-only run:
// git interleaves --name-only's file list for commit N into the START of
// commit N+1's output when a custom --format is used, which makes a single
// positional parse fragile. Two clean, independently-delimited streams
// avoid that entirely.
func Collect(limit int) (Stats, error) {
	stats := Stats{MethodCount: map[spec.Method]int{}, PurposeCount: map[purpose.Purpose]int{}}
	prefix := spec.TrailerKey + ":"

	format := "%H" + fieldSep + "%aI" + fieldSep + "%B" + recordSep
	metaOut, err := exec.Command("git", "log", "-n", fmt.Sprint(limit), "--format="+format).Output()
	if err != nil {
		// An empty/unborn repo (no commits yet) is a valid, empty report,
		// not a failure — anything else genuinely is.
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return stats, nil
		}
		return Stats{}, fmt.Errorf("read git log: %w", err)
	}

	filesBySHA, err := commitFiles(limit)
	if err != nil {
		return Stats{}, err
	}

	sizesBySHA, err := commitSizes(limit)
	if err != nil {
		return Stats{}, err
	}

	for _, record := range strings.Split(string(metaOut), recordSep) {
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}
		sha, dateStr, rest := parts[0], parts[1], strings.TrimRight(parts[2], "\n")
		subject := strings.SplitN(rest, "\n", 2)[0]

		ts, _ := time.Parse(time.RFC3339, dateStr) // zero time on parse failure, sorts as oldest
		files := filesBySHA[sha]

		size := sizesBySHA[sha]
		c := Commit{
			SHA:          sha,
			Timestamp:    ts,
			Subject:      subject,
			Files:        files,
			Purpose:      purpose.Classify(rest, files),
			LinesAdded:   size.added,
			LinesRemoved: size.removed,
		}
		if t, ok := findTrailer(rest, prefix); ok {
			c.Trailer = &t
			stats.Covered++
			stats.MethodCount[t.Method]++
			if t.Status == spec.StatusAssisted {
				stats.Assisted++
			}
		}
		stats.PurposeCount[c.Purpose]++
		stats.TotalCommits++
		stats.Commits = append(stats.Commits, c)
	}

	return stats, nil
}

type commitSize struct{ added, removed int }

// commitSizes returns each commit's diff size, keyed by sha.
//
// A separate pass for the same reason as commitFiles: git interleaves
// --numstat output awkwardly with a custom --format, and two clean streams
// beat one fragile positional parse.
func commitSizes(limit int) (map[string]commitSize, error) {
	out, err := exec.Command("git", "log", "-n", fmt.Sprint(limit),
		"--format=COMMIT %H", "--numstat").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return map[string]commitSize{}, nil
		}
		return nil, fmt.Errorf("read git log --numstat: %w", err)
	}

	result := map[string]commitSize{}
	var sha string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "COMMIT ") {
			sha = strings.TrimPrefix(line, "COMMIT ")
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || sha == "" {
			continue
		}
		// Binary files report "-" for both counts. Skipping them is
		// correct: they have no line count, and counting them as zero
		// would be indistinguishable from an empty text change.
		a, errA := strconv.Atoi(fields[0])
		r, errR := strconv.Atoi(fields[1])
		if errA != nil || errR != nil {
			continue
		}
		size := result[sha]
		size.added += a
		size.removed += r
		result[sha] = size
	}
	return result, nil
}

// commitFiles returns, for each of the last `limit` commits, the list of
// files it touched — keyed by full sha, using git's own "COMMIT <sha>"
// marker rather than a custom --format so --name-only's file list stays
// unambiguously scoped to the commit right above it.
func commitFiles(limit int) (map[string][]string, error) {
	out, err := exec.Command("git", "log", "-n", fmt.Sprint(limit), "--format=COMMIT %H", "--name-only").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("read git log --name-only: %w", err)
	}

	result := map[string][]string{}
	var currentSHA string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "COMMIT "):
			currentSHA = strings.TrimPrefix(line, "COMMIT ")
		case strings.TrimSpace(line) != "" && currentSHA != "":
			result[currentSHA] = append(result[currentSHA], line)
		}
	}
	return result, nil
}

func findTrailer(commitMsg, prefix string) (spec.Trailer, bool) {
	scanner := bufio.NewScanner(strings.NewReader(commitMsg))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		t, err := spec.Parse(strings.TrimSpace(line[len(prefix):]))
		if err != nil {
			continue
		}
		return t, true
	}
	return spec.Trailer{}, false
}
