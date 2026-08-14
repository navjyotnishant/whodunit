package agy

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// agy is the most expensive adapter to scope, and the reason is
// structural: it records no workspace directory this adapter can trust, so
// deciding whether a conversation belongs to a repository means opening
// its database and reading its edits. That runs on the commit path, once
// per conversation on the machine.
//
// These assert budgets rather than printing numbers. The budgets are loose
// on purpose — they catch a change in complexity, not a wall-clock target,
// because a budget tight enough to be a target is a budget that flakes on
// a shared runner.

func TestSessionFilesStaysWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	root := writePerfConversations(t, 40, 25)
	t.Setenv("WHODUNIT_AGY_PATH", root)

	const budget = 15 * time.Second

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
		t.Errorf("SessionFiles over 40 conversations took %v, budget %v", elapsed, budget)
	}
}

// Reading steps must scale with the number of steps, not with their
// square. Best-of-N on both sides: a single timing on a shared runner is
// mostly noise.
func TestParseSinceScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	measure := func(steps int) time.Duration {
		path := writePerfConversation(t, "/repo/target", steps)
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

	small := measure(50)
	large := measure(500)

	const ceiling = 30.0
	ratio := float64(large) / float64(small)
	if ratio > ceiling {
		t.Errorf("10x the steps cost %.1fx the time (%v then %v); expected roughly linear, ceiling %.0fx",
			ratio, small, large, ceiling)
	}
}

// Reading steps.status (NAV-84) added a column to a query that runs on
// every step of every conversation. It must not have made scoping
// measurably worse — the column is indexed and in the same row, so the
// cost should be indistinguishable.
func TestStatusColumnDoesNotSlowScoping(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	path := writePerfConversation(t, "/repo/target", 400)

	const budget = 5 * time.Second

	best := time.Duration(1<<62 - 1)
	for i := 0; i < 5; i++ {
		start := time.Now()
		calls, err := readCalls(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) == 0 {
			t.Fatal("read nothing")
		}
		if e := time.Since(start); e < best {
			best = e
		}
	}
	if best > budget {
		t.Errorf("readCalls over 400 steps took %v, budget %v", best, budget)
	}
}

// A conversation that cannot be read must fail fast rather than hanging
// the commit. One locked or corrupt database must not hide the others, and
// it must not cost the commit path a timeout either.
func TestUnreadableConversationFailsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	path := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	const budget = 5 * time.Second

	start := time.Now()
	_, _ = ParseSince(path, time.Time{})
	if elapsed := time.Since(start); elapsed > budget {
		t.Errorf("an unreadable conversation took %v to give up, budget %v", elapsed, budget)
	}
}

// writePerfConversations lays out n conversation databases, only one of
// which belongs to the repository being searched for — the realistic
// shape, since a developer's conversations span many repositories.
func writePerfConversations(tb testing.TB, n, steps int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := 0; i < n; i++ {
		cwd := fmt.Sprintf("/repo/other%d", i)
		if i == n/2 {
			cwd = "/repo/target"
		}
		path := filepath.Join(root, fmt.Sprintf("%08d-0000-0000-0000-000000000000.db", i))
		writeConversationAt(tb, path, cwd, steps)
	}
	return root
}

func writePerfConversation(tb testing.TB, cwd string, steps int) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "conversation.db")
	writeConversationAt(tb, path, cwd, steps)
	return path
}

// writeConversationAt builds a database with agy's schema and one
// write_file step per iteration. The payload is the embedded JSON the
// adapter scans for; the protobuf framing around it does not matter to
// what is being measured.
func writeConversationAt(tb testing.TB, path, cwd string, steps int) {
	tb.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		tb.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE steps (
		idx integer PRIMARY KEY,
		step_type integer NOT NULL DEFAULT 0,
		status integer NOT NULL DEFAULT 0,
		has_subtrajectory numeric NOT NULL DEFAULT false,
		metadata blob, error_details blob, permissions blob,
		task_details blob, render_info blob, step_payload blob,
		step_format integer NOT NULL DEFAULT 0)`); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_steps_status ON steps(status)`); err != nil {
		tb.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO steps (idx, step_type, status, step_payload) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < steps; i++ {
		// Statuses in the proportion observed in real databases: mostly
		// success, a few rejections.
		status := statusSuccess
		if i%50 == 7 {
			status = statusRejected
		}
		payload := fmt.Sprintf(
			`{"TargetFile":"%s/file%d.go","CodeContent":"package main\n\nfunc main() {}\n"}`,
			cwd, i)
		if _, err := stmt.Exec(i, 5, status, []byte(payload)); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}
