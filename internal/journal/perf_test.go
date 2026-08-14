// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Budgets for the journal queries the commit hook runs.

package journal

import (
	"fmt"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/testmode"
)

// The journal is the one thing here that grows without bound. Everything
// else — a diff, a transcript, a commit — is bounded by the change being
// made, but the journal accumulates every edit an agent has ever made in
// every repository on the machine.
//
// So the queries the commit hook runs against it are the ones worth
// bounding. They are indexed and should scale with what the repository has
// recorded rather than with the size of the file; a regression that turned
// one into a table scan would be invisible on a fresh install and painful
// after six months of use, which is the worst way for a performance bug to
// arrive.
//
// The budgets are deliberately loose — well above what a correct
// implementation produces on a shared CI runner, and set to catch a change
// of the wrong order rather than to police milliseconds. A flaky gate gets
// disabled, and a disabled gate is worse than none because it still implies
// protection.
const (
	// readLineHashesBudget covers the query the prepare-commit-msg hook runs
	// on every commit, over the 30-day attribution window.
	readLineHashesBudget = 1500 * time.Millisecond

	// countSinceBudget covers what `dun status` runs, which is interactive:
	// a person is watching it.
	countSinceBudget = 500 * time.Millisecond
)

// seedManyEntries writes n entries, each carrying line hashes, spread over
// the last 20 days.
//
// Inside the hook's 30-day window on purpose: the query filters on the
// timestamp, so seeding beyond it measures how fast the index rejects rows
// rather than how fast the real query returns them. The first version of
// this spread entries over 60 days and returned nothing at all.
func seedManyEntries(tb testing.TB, dataDir string, n int) {
	tb.Helper()

	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		tb.Fatal(err)
	}
	defer w.Close()

	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		// Timestamps must differ per entry: the uniqueness constraint
		// includes the timestamp, so reusing one silently collapses the
		// seed. Minutes back rather than hours keeps 20k entries inside the
		// 30-day window the hook queries.
		ts := now.Add(-time.Duration(i) * time.Minute)

		if err := w.Append(Entry{
			Timestamp:    ts,
			Agent:        "claude-code",
			Session:      fmt.Sprintf("sess-%d", i/50),
			Event:        "tool_use",
			Tool:         "Edit",
			File:         fmt.Sprintf("/repo/pkg%d/file%d.go", i%20, i),
			LinesAdded:   7,
			HunkHash:     fmt.Sprintf("sha256:%d", i),
			SpecVersion:  SpecVersion,
			AgentVersion: "2.0.0",
		}); err != nil {
			tb.Fatalf("seed entry %d: %v", i, err)
		}

		// Line hashes go through their own call — Entry.LineHashes is
		// carried by the adapters, but the journal writes the agent_lines
		// table from AppendLines. Seeding without this produced an empty
		// table and a budget test that measured nothing.
		//
		// Seven per entry, which is what this project's own history
		// averages; agent_lines is the bulk of the journal.
		hashes := make([]uint64, 7)
		for j := range hashes {
			hashes[j] = uint64(i*7 + j)
		}
		if err := w.AppendLines(hashes, ts); err != nil {
			tb.Fatalf("seed line hashes %d: %v", i, err)
		}
	}
}

func TestReadLineHashesStaysWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("seeding takes a few seconds")
	}
	dataDir := t.TempDir()
	// 20k entries is roughly eight months of this project's own rate.
	seedManyEntries(t, dataDir, 20000)

	since := time.Now().Add(-30 * 24 * time.Hour)

	start := time.Now()
	hashes, err := ReadLineHashes(dataDir, testRepo, since)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) == 0 {
		t.Fatal("no hashes returned; the query is not exercising anything")
	}
	// Timing skipped under -race: the detector's overhead is what would be
	// measured. The assertions above still run, so the query is still
	// checked for returning the right rows.
	if !testmode.RaceEnabled && elapsed > readLineHashesBudget {
		t.Fatalf("ReadLineHashes over %d hashes took %v, past the %v budget. "+
			"This runs on every commit and the journal only grows.",
			len(hashes), elapsed, readLineHashesBudget)
	}
	t.Logf("ReadLineHashes: %d hashes in %v (budget %v)", len(hashes), elapsed, readLineHashesBudget)
}

func TestCountSinceStaysWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("seeding takes a few seconds")
	}
	dataDir := t.TempDir()
	seedManyEntries(t, dataDir, 20000)

	start := time.Now()
	entries, sessions, err := CountSince(dataDir, testRepo, time.Now().Add(-24*time.Hour))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if entries == 0 || sessions == 0 {
		t.Fatalf("counted %d entries / %d sessions; the query found nothing to count",
			entries, sessions)
	}
	if !testmode.RaceEnabled && elapsed > countSinceBudget {
		t.Fatalf("CountSince took %v, past the %v budget. `dun status` runs "+
			"this with someone watching.", elapsed, countSinceBudget)
	}
	t.Logf("CountSince: %d entries, %d sessions in %v", entries, sessions, elapsed)
}

// The point of the index: a query scoped to one repository must not get
// slower because *other* repositories recorded more.
//
// This is the regression that would hurt most and show least. A developer's
// own repository stays small while the machine's journal grows with every
// other project they touch, so a query that degraded with total rows would
// look fine in every test and get slower every month in real use.
func TestReadLineHashesScalesWithTheRepoNotTheFile(t *testing.T) {
	if testing.Short() {
		t.Skip("seeding takes a few seconds")
	}
	dataDir := t.TempDir()

	// The repository under test: a fixed, small amount of history.
	seedManyEntries(t, dataDir, 2000)

	since := time.Now().Add(-30 * 24 * time.Hour)

	// Best of five, not a single reading.
	//
	// One measurement of a few milliseconds is mostly page-cache state: the
	// first version of this test read 5ms alone and 27ms crowded and called
	// it a 5x regression, when repeating each side gives 1.00x — the index
	// is exact and the difference was a cold cache on a file that had just
	// grown tenfold. A gate that reports a scan where there is none gets
	// disabled, and then it protects nothing.
	fastest := func() time.Duration {
		var best time.Duration
		for i := 0; i < 5; i++ {
			start := time.Now()
			if _, err := ReadLineHashes(dataDir, testRepo, since); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); best == 0 || d < best {
				best = d
			}
		}
		return best
	}

	before, err := ReadLineHashes(dataDir, testRepo, since)
	if err != nil {
		t.Fatal(err)
	}
	alone := fastest()

	// Ten other repositories pile in around it.
	for r := 0; r < 10; r++ {
		w, err := NewWriter(dataDir, fmt.Sprintf("other-repo-%d", r))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		for i := 0; i < 2000; i++ {
			ts := now.Add(-time.Duration(i) * time.Minute)
			if err := w.Append(Entry{
				Timestamp: ts,
				Agent:     "claude-code",
				Session:   fmt.Sprintf("other-%d-%d", r, i/50),
				Event:     "tool_use",
				Tool:      "Edit",
				File:      fmt.Sprintf("/other%d/file%d.go", r, i),
			}); err != nil {
				t.Fatal(err)
			}
			if err := w.AppendLines([]uint64{uint64(r)<<40 | uint64(i)}, ts); err != nil {
				t.Fatal(err)
			}
		}
		w.Close()
	}

	after, err := ReadLineHashes(dataDir, testRepo, since)
	if err != nil {
		t.Fatal(err)
	}
	crowded := fastest()

	if len(after) != len(before) {
		t.Fatalf("the query returned %d hashes alone and %d with other "+
			"repositories present; it is not scoped to one repository",
			len(before), len(after))
	}

	// Ten times the rows must not mean anything like ten times the time.
	// The multiple is generous because both measurements are small and a
	// shared runner is noisy; what it rules out is a linear scan.
	// Best-of-five on both sides makes a tight bound meaningful: the
	// measured ratio is 1.00x, so 3x is a wide margin that still catches a
	// query degrading into a scan.
	const tolerated = 3
	if !testmode.RaceEnabled && crowded > alone*tolerated && crowded > 50*time.Millisecond {
		t.Fatalf("reading this repository's hashes took %v alone and %v with "+
			"10x unrelated rows present — more than %dx. The query is "+
			"scanning rows that belong to other repositories.",
			alone, crowded, tolerated)
	}
	t.Logf("ReadLineHashes: %v alone, %v with 10x unrelated rows", alone, crowded)
}
