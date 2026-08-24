// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The panic barrier, and what dun log shows.

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/hooklog"
)

// NAV-75 criterion 10, and the one failure mode worse than wrong
// attribution. Go has no exceptions; a runtime panic anywhere in the hook
// path would take down the process mid-commit, and "a commit never fails
// because attribution failed" does not survive that.
func TestAPanicInAHookDoesNotFailTheCommit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	// The panic the barrier has to catch, injected rather than compiled
	// into the shipped binary.
	restore := hookProbe
	t.Cleanup(func() { hookProbe = restore })
	hookProbe = func(string) { panic("hook panic probe") }

	cmd := newHookCmd()
	cmd.SetArgs([]string{hookPrepare})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("a panicking hook returned an error and would have failed "+
			"the commit: %v", err)
	}

	// Recovering silently would be worse than crashing: it trades a visible
	// failure for an invisible no-op.
	entries, err := hooklog.Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Level != hooklog.LevelPanic {
		t.Fatalf("the recovered panic was not recorded: %+v", entries)
	}
	if entries[0].Stack == "" {
		t.Error("no stack was recorded")
	}
}

// The barrier itself, called directly: a recovered panic must be recorded
// with its stack, or recovery is a crash traded for a silent no-op.
func TestARecoveredPanicIsLoggedWithItsStack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	func() {
		defer func() {
			if r := recover(); r != nil {
				logPanic("prepare-commit-msg", r, []byte("goroutine 1 [running]:\nmain.boom()"))
			}
		}()
		var m map[string]int
		m["write to a nil map"] = 1 // panics
	}()

	entries, err := hooklog.Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the recovered panic", len(entries))
	}
	if entries[0].Level != hooklog.LevelPanic {
		t.Errorf("the panic was recorded as %q", entries[0].Level)
	}
	if entries[0].Stack == "" {
		t.Error("no stack was recorded; a panic without one leaves the reader " +
			"exactly where the crash did")
	}
	if !strings.Contains(entries[0].Detail, "nil map") {
		t.Errorf("the panic value was not recorded: %q", entries[0].Detail)
	}
}

// Criterion 7: an empty log must say so and name what would fill it.
// Printing nothing makes an empty log and a broken one identical.
func TestAnEmptyLogExplainsItself(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	var out bytes.Buffer
	if err := runLog(&out, "", 50, false, false); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "nothing recorded yet") {
		t.Fatalf("an empty log printed nothing useful:\n%s", s)
	}
	if !strings.Contains(s, "dun verify") {
		t.Errorf("it did not name what would produce entries:\n%s", s)
	}
}

// Criterion 1, the point of the whole issue: an error a hook swallowed has
// to be findable afterwards.
func TestASwallowedErrorIsInTheLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	logHook("prepare-commit-msg", hooklog.LevelWarn, "determine",
		"unreadable codex transcript rollout-1.jsonl: unexpected EOF")

	var out bytes.Buffer
	if err := runLog(&out, "", 50, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unexpected EOF") {
		t.Fatalf("the swallowed error is not in the log:\n%s", out.String())
	}
}

// Criterion 3's filter: --errors hides the routine entries so a problem is
// not buried under every successful commit.
func TestErrorsOnlyHidesRoutineEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	logHook("prepare-commit-msg", hooklog.LevelInfo, "determine", "assisted via intersected")
	logHook("pre-push", hooklog.LevelWarn, "sync", "connection refused")

	var out bytes.Buffer
	if err := runLog(&out, "", 50, false, true); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "assisted via intersected") {
		t.Errorf("--errors showed a routine entry:\n%s", s)
	}
	if !strings.Contains(s, "connection refused") {
		t.Errorf("--errors hid the failure:\n%s", s)
	}
}

// The log must never carry prompt text or file contents (NAV-25). Nothing
// in the logging API accepts them, and this asserts the entries the hooks
// actually write stay within paths and counts.
func TestTheLogHoldsNoMessageContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	repo := newRepo(t, "log-privacy")
	realRepo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(realRepo)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	determineTrailer("")

	entries, err := hooklog.Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("determineTrailer recorded nothing in this environment")
	}
	for _, e := range entries {
		if len(e.Detail) > 300 {
			t.Errorf("a log detail is %d chars, long enough to be carrying "+
				"content rather than a reason: %q", len(e.Detail), e.Detail)
		}
	}
}
