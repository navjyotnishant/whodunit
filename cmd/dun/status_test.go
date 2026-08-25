package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunStatusOnEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	c := &cobra.Command{}
	buf := &bytes.Buffer{}
	c.SetOut(buf)

	if err := runStatus(c, ""); err != nil {
		t.Fatalf("runStatus() on empty repo = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "commits examined:  0") {
		t.Errorf("output = %q, want it to report 0 commits", buf.String())
	}
}

func TestRunStatusReportsCoverageAndMethodMix(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "--allow-empty", "-q", "-m", "with trailer",
		"-m", "AI-Attribution: status=assisted; method=observed; agent=claude-code")
	run("commit", "--allow-empty", "-q", "-m", "no trailer")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	c := &cobra.Command{}
	buf := &bytes.Buffer{}
	c.SetOut(buf)

	if err := runStatus(c, ""); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "commits examined:  2") {
		t.Errorf("output missing commit count: %s", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("output missing 1/2 coverage: %s", out)
	}
	if !strings.Contains(out, "observed") {
		t.Errorf("output missing method mix row: %s", out)
	}
}

// A repository instrumented partway through its life has commits that
// predate attribution. Those are not evidence that no agent was used, and
// the coverage figure alone invites exactly that reading (NAV-21): 1/3
// covered looks like two commits somebody typed by hand.
func TestRunStatusReportsUnattributedSpan(t *testing.T) {
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	at := func(d string) []string {
		return []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}
	}
	run(nil, "init", "-q")
	// Two commits before whodunit existed here, then one after.
	run(at("2026-01-01T00:00:00Z"), "commit", "--allow-empty", "-q", "-m", "before one")
	run(at("2026-01-02T00:00:00Z"), "commit", "--allow-empty", "-q", "-m", "before two")
	run(at("2026-06-15T00:00:00Z"), "commit", "--allow-empty", "-q", "-m", "after",
		"-m", "AI-Attribution: status=assisted; method=observed; agent=claude-code")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	c := &cobra.Command{}
	buf := &bytes.Buffer{}
	c.SetOut(buf)
	if err := runStatus(c, ""); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "attribution began 2026-06-15") {
		t.Errorf("output missing the attribution boundary: %s", out)
	}
	if !strings.Contains(out, "2 older commit(s)") {
		t.Errorf("output missing the unattributed count: %s", out)
	}
	// The wording carries the whole point. "unknown, not absent" is the
	// difference between reporting a gap and asserting nobody used AI.
	if !strings.Contains(out, "unknown, not absent") {
		t.Errorf("output must not let absence read as non-use: %s", out)
	}
}

// A repository whose every scanned commit carries a trailer has no gap to
// report, and saying so anyway would be noise on the common path.
func TestRunStatusOmitsSpanWhenFullyAttributed(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "--allow-empty", "-q", "-m", "only commit",
		"-m", "AI-Attribution: status=assisted; method=observed; agent=claude-code")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	c := &cobra.Command{}
	buf := &bytes.Buffer{}
	c.SetOut(buf)
	if err := runStatus(c, ""); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if strings.Contains(buf.String(), "attribution began") {
		t.Errorf("no gap exists; the line should be absent: %s", buf.String())
	}
}
