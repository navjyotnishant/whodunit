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
