// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Keeps the test suite out of the real ~/.whodunit.

package main

import (
	"os"
	"testing"
)

// TestMain points every test at a throwaway home.
//
// Found by running the suite and then reading the real log: entries named
// TestRunPrepareCommitMsgAppendsTrailer had been written into
// ~/.whodunit/log, because tests that never set WHODUNIT_HOME resolve to
// the developer's actual home directory.
//
// Every affected test could set it individually, and most already do — but
// that is a rule someone has to remember on every new test, and forgetting
// it corrupts real data rather than failing. A default here makes the
// isolation structural: a test has to opt out to reach the real home, and
// nothing does.
//
// t.Setenv in an individual test still overrides this, so tests that set
// their own home are unaffected.
func TestMain(m *testing.M) {
	// A hook installed by a test points at this binary.
	//
	// `dun init` writes a hook of the form
	//
	//     DUN="$(command -v dun || echo "<the binary that ran init>")"
	//     "$DUN" hook prepare-commit-msg "$@"
	//
	// and in a test that fallback is the compiled test binary. On a
	// developer machine `command -v dun` finds the installed release and
	// the fallback never fires, so this is invisible. On CI, where nothing
	// is on PATH, git runs the test binary — which re-enters the test
	// framework and runs the whole suite again inside a commit hook. The
	// suite deadlocked there for the full ten-minute timeout.
	//
	// Rather than making tests avoid committing, honour the invocation:
	// when argv says "hook", be dun.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		if err := newRootCmd().Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if os.Getenv("WHODUNIT_HOME") == "" {
		dir, err := os.MkdirTemp("", "whodunit-test-home")
		if err != nil {
			panic(err)
		}
		os.Setenv("WHODUNIT_HOME", dir)

		// Cleaned up before os.Exit, which does not run deferred functions.
		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
	os.Exit(m.Run())
}
