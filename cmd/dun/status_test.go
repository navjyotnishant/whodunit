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
