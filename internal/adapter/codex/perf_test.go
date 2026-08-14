package codex

import (
	"testing"
	"time"
)

// The benchmarks next door print numbers. These assert budgets, and fail
// the build when one is exceeded — a benchmark nobody reads is not a gate.
//
// The budgets are deliberately loose. They exist to catch a regression in
// complexity (an accidental O(n²), a per-line allocation, reading a file
// twice), not to hold a wall-clock figure on a shared CI runner. A budget
// tight enough to be a performance target is a budget that flakes.

// SessionFiles runs on the commit path and its cost grows with the number
// of transcripts on the machine, not with the repository being committed
// to. A developer who uses Codex heavily pays this on every commit.
func TestSessionFilesStaysWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	root := writeBenchSessions(t, 300)
	t.Setenv("WHODUNIT_CODEX_PATH", root)

	const budget = 10 * time.Second

	start := time.Now()
	files, err := SessionFiles("/repo/target")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("matched nothing — the test is measuring the wrong thing")
	}
	if elapsed > budget {
		t.Errorf("SessionFiles over 300 transcripts took %v, budget %v", elapsed, budget)
	}
}

// Scaling matters more than the absolute figure: if this is linear, ten
// times the transcripts costs ten times as much and the machine stays
// usable. If it is quadratic, a heavy Codex user's commits get slower
// every week for reasons nobody connects to whodunit.
//
// Best-of-N on both sides. A single timing on a shared runner is mostly
// noise — an earlier version of a test like this reported a 5x regression
// that was entirely cold page cache.
func TestParseSinceScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	measure := func(patches int) time.Duration {
		path := writeBenchRollout(t, patches)
		best := time.Duration(1<<62 - 1)
		for i := 0; i < 5; i++ {
			start := time.Now()
			entries, err := ParseSince(path, time.Time{})
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) == 0 {
				t.Fatal("parsed nothing")
			}
			if elapsed < best {
				best = elapsed
			}
		}
		return best
	}

	small := measure(200)
	large := measure(2000)

	// 10x the input. Linear would be ~10x; the ceiling allows for fixed
	// costs and runner noise while still catching a quadratic curve, which
	// would land near 100x.
	const ceiling = 30.0
	ratio := float64(large) / float64(small)
	if ratio > ceiling {
		t.Errorf("10x the patches cost %.1fx the time (%v then %v); expected roughly linear, ceiling %.0fx",
			ratio, small, large, ceiling)
	}
}

// A transcript entirely older than the cutoff must be cheap to skip. If
// the cutoff is consulted only after the file is parsed, a no-op ingest
// costs as much as a real one — and a no-op ingest is the common case,
// since most commits touch transcripts that were already read.
func TestParseSinceSkipsOldTranscriptsCheaply(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	path := writeBenchRollout(t, 2000)

	bestOf := func(since time.Time) time.Duration {
		best := time.Duration(1<<62 - 1)
		for i := 0; i < 5; i++ {
			start := time.Now()
			if _, err := ParseSince(path, since); err != nil {
				t.Fatal(err)
			}
			if e := time.Since(start); e < best {
				best = e
			}
		}
		return best
	}

	full := bestOf(time.Time{})
	skipped := bestOf(time.Now().Add(24 * time.Hour))

	if skipped > full {
		t.Errorf("skipping everything (%v) cost more than parsing everything (%v)", skipped, full)
	}
}

// The MCP fix added a second field to the tool-call path (NAV-83). It runs
// on every function_call in every transcript, so it must not have made the
// common case measurably worse.
func TestMCPResolutionIsNotOnTheCriticalPath(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	const iterations = 1_000_000
	const budget = 500 * time.Millisecond

	start := time.Now()
	for i := 0; i < iterations; i++ {
		mcpTool("", "shell")
		mcpTool("mcp__linear", "save_comment")
	}
	elapsed := time.Since(start)

	if elapsed > budget {
		t.Errorf("%d mcpTool resolutions took %v, budget %v", iterations*2, elapsed, budget)
	}
}
