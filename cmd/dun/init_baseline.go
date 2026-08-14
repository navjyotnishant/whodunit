// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The pre-adoption snapshot `dun init` takes before it installs
// hooks.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/repoid"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// baselineWindowDays is how far back the automatic snapshot looks.
//
// Ninety days rather than the whole history: a repository's delivery rhythm
// three years ago says little about the month before adoption, and a long
// window buries a recent change in an average. Someone who wants a different
// window can still run `dun baseline capture --days N` before init.
const baselineWindowDays = 90

// captureBaselineOnInit records what delivery looked like before attribution
// started, and never fails the install.
//
// Called at the top of `dun init`, before a single hook is written, because
// this measurement has exactly one opportunity. Once commits start carrying
// trailers the unassisted population stops growing, and a before/after
// comparison is asking about a window that no longer exists. Git history
// stays available, but the question "what did this repository look like
// before AI" cannot be re-asked later — the answer changes the moment the
// hooks run.
//
// Silent on every failure by design. Instrumenting a repository is the job;
// a snapshot that could not be taken is worth a line of output, never a
// refused install.
func captureBaselineOnInit(w io.Writer, repoPath string) {
	c := termcolor.New(w)

	path, err := baselinePathFor(repoPath)
	if err != nil {
		// No repository id yet — a repo with no commits. There is also no
		// history to measure, so there is nothing to say.
		return
	}

	// Say nothing when a baseline already exists.
	//
	// Immutability itself is enforced by baseline.Write, which refuses to
	// replace a snapshot — that is the guarantee, and it holds whether or
	// not this check is here. What this avoids is the noise: `dun init` is
	// re-run routinely, after an upgrade or to repair hooks, and without
	// this every one of those runs would print "no pre-adoption baseline
	// captured: refusing to overwrite…" as though something had gone wrong.
	if _, err := os.Stat(path); err == nil {
		return
	}

	snap, err := captureIn(repoPath, baselineWindowDays)
	if err != nil {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"no pre-adoption baseline captured: "+err.Error()))
		return
	}
	if snap.Git.Commits == 0 {
		// An empty window measures nothing. Writing it would satisfy the
		// "does a baseline exist" check while comparing against zero.
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			fmt.Sprintf("no pre-adoption baseline: no commits in the last %d days",
				baselineWindowDays)))
		return
	}
	if err := baseline.Write(path, snap); err != nil {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"no pre-adoption baseline captured: "+err.Error()))
		return
	}

	fmt.Fprintf(w, "%s %s\n",
		c.S(termcolor.Good, "captured a pre-adoption baseline"),
		c.S(termcolor.Muted, fmt.Sprintf("(%d commits over %d days, %.1f/week)",
			snap.Git.Commits, snap.WindowDays, snap.Git.CommitsPerWeek)))
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"this is the only chance to measure delivery before attribution starts"))
}

// baselinePathFor resolves where a repository's snapshot belongs.
//
// Separate from defaultBaselinePath because that one reads the working
// directory, and `dun init --repo` instruments a repository somewhere else.
func baselinePathFor(repoPath string) (string, error) {
	dir, err := config.BaselinesDir()
	if err != nil {
		return "", err
	}
	repoID, err := repoid.ForRepo(repoPath)
	if err != nil {
		return "", err
	}
	if err := config.EnsureDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, repoID+".json"), nil
}

// captureIn runs baseline.Capture against a specific repository.
//
// Capture reads the current directory, so instrumenting another repository
// means going there and coming back. The directory is restored even when the
// capture fails, since `dun init` has more to do afterwards.
func captureIn(repoPath string, days int) (baseline.Snapshot, error) {
	if repoPath == "" {
		return baseline.Capture(days, nil, time.Now())
	}

	prev, err := os.Getwd()
	if err != nil {
		return baseline.Snapshot{}, err
	}
	if err := os.Chdir(repoPath); err != nil {
		return baseline.Snapshot{}, err
	}
	defer os.Chdir(prev)

	return baseline.Capture(days, nil, time.Now())
}
