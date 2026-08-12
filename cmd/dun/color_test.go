package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// containsANSI reports whether s carries any escape sequence. Checking for
// the ESC byte rather than a specific code catches styling added later by
// any means, which is the point: these tests exist to fail when someone
// colorizes a surface that must stay plain.
func containsANSI(s string) bool { return strings.Contains(s, "\x1b[") }

// NAV-50 criterion 5: the commit-message hooks must never write color.
// A trailer is appended to COMMIT_EDITMSG; an escape sequence there ends
// up committed into git history, where it is permanent.
func TestHookOutputHasNoANSI(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1") // even when color is explicitly demanded

	dir := t.TempDir()
	msgPath := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgPath, []byte("feat: something\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-q", "-m", "base")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"hook", "prepare-commit-msg", msgPath})
	// The hook must not fail the commit, but this test is about what it
	// writes, so an error here is reported rather than ignored.
	if err := cmd.Execute(); err != nil {
		t.Logf("hook returned %v (not fatal for this test)", err)
	}

	written, err := os.ReadFile(msgPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsANSI(string(written)) {
		t.Fatalf("commit message file contains ANSI escapes:\n%q", written)
	}
	if containsANSI(out.String()) {
		t.Fatalf("hook stdout contains ANSI escapes:\n%q", out.String())
	}
}

// NAV-50 criterion 6: `dun check` is the gate a PR blocks on. Its output
// is read out of a CI log, so it must stay unstyled and parseable.
func TestCheckOutputHasNoANSI(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-q", "-m", "base")
	git("branch", "base")
	git("commit", "--allow-empty", "-q", "-m", "no trailer here")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// runCheck reports the failure through its error; the output is what
	// this test cares about either way.
	_ = runCheck(cmd, "base")

	if containsANSI(out.String()) {
		t.Fatalf("check output contains ANSI escapes:\n%q", out.String())
	}
}

// Criterion 4, end to end: a command writing to a buffer produces no
// escapes, which is what every pipe and redirect looks like.
func TestWelcomeToBufferHasNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	if err := runWelcome(&out); err != nil {
		t.Fatal(err)
	}
	if containsANSI(out.String()) {
		t.Fatalf("welcome output contains ANSI escapes:\n%q", out.String())
	}
	if !strings.Contains(out.String(), "whodunit") {
		t.Fatalf("welcome output missing the product name:\n%s", out.String())
	}
}

// Criterion 3: the welcome screen must name a next command, or it is just
// a banner.
func TestWelcomeNamesANextCommand(t *testing.T) {
	var out bytes.Buffer
	if err := runWelcome(&out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "dun init") &&
		!strings.Contains(s, "dun status") &&
		!strings.Contains(s, "dun repos list") {
		t.Fatalf("welcome screen suggests no next command:\n%s", s)
	}
}
