package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Re-running init in an instrumented repository must say so. Three lines
// of "installed ..." read identically whether this is a first install or
// the fourth, so the one question a re-run asks — is this set up? — goes
// unanswered.
func TestInitReportsAnAlreadyInstrumentedRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.local")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-q", "-m", "first")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	run := func() string {
		c := &cobra.Command{}
		buf := &bytes.Buffer{}
		c.SetOut(buf)
		if err := runInit(c, ""); err != nil {
			t.Fatalf("runInit: %v", err)
		}
		return buf.String()
	}

	first := run()
	if strings.Contains(first, "already instrumented") {
		t.Errorf("a first install must not claim prior instrumentation:\n%s", first)
	}

	second := run()
	if !strings.Contains(second, "already instrumented") {
		t.Errorf("a re-run must say the repository is already set up:\n%s", second)
	}
	// Still repairs. A re-run is how hooks are restored after an upgrade
	// or after another tool overwrote them, so reporting must not replace
	// the work.
	if !strings.Contains(second, "installed prepare-commit-msg") {
		t.Errorf("a re-run must still rewrite the hooks:\n%s", second)
	}
}

// The pre-adoption window closes the moment hooks start stamping. Capturing
// a baseline on a re-run would record one that already contains assisted
// work and label it "before".
func TestInitDoesNotRecaptureTheBaselineOnARerun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.local")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-q", "-m", "first")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	run := func() string {
		c := &cobra.Command{}
		buf := &bytes.Buffer{}
		c.SetOut(buf)
		if err := runInit(c, ""); err != nil {
			t.Fatalf("runInit: %v", err)
		}
		return buf.String()
	}
	run()

	// Asserted on the capture line specifically, not on the phrase
	// "pre-adoption baseline" — that also appears in explanatory output,
	// so matching it would fail on prose rather than on behaviour.
	if second := run(); strings.Contains(second, "captured a pre-adoption baseline") {
		t.Errorf("a re-run must not recapture the baseline:\n%s", second)
	}
}
