package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunCheck(t *testing.T) {
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
	run("commit", "--allow-empty", "-q", "-m", "base")
	run("branch", "base")

	run("commit", "--allow-empty", "-q", "-m", "good\n\nAI-Attribution: status=undetermined; method=undetermined")
	run("commit", "--allow-empty", "-q", "-m", "missing trailer")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	err := runCheck(cmd, "base")
	if err == nil {
		t.Fatal("runCheck() = nil error, want error for missing trailer")
	}
	if !bytes.Contains(buf.Bytes(), []byte("missing")) {
		t.Errorf("output missing expected message: %s", buf.String())
	}
}

// A merge commit is not a missing trailer.
//
// It introduces no changes of its own — its content is its parents, each
// already checked on the branch it came from — so there is nothing to
// attribute even in principle. And the merges that reach a default branch
// are usually made server-side by a "Merge pull request" button, where no
// local hook runs at all.
//
// This is asserted rather than trusted to `--no-merges` staying in the
// command, because the failure is invisible until a pull request is opened
// and then permanent: the check reports the merge on every subsequent PR,
// and the fix people reach for is to stop requiring the check.
//
// It happened here — three merge commits on this repository's own history,
// one of them the release that had just shipped.
func TestRunCheckIgnoresMergeCommits(t *testing.T) {
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

	trailer := "\n\nAI-Attribution: status=undetermined; method=undetermined"

	// -b main rather than relying on the default: git's initial branch
	// name depends on init.defaultBranch, so a hardcoded "master" fails on
	// any machine configured otherwise — including this one.
	run("init", "-q", "-b", "main")
	run("commit", "--allow-empty", "-q", "-m", "base"+trailer)
	run("branch", "base")

	// A side branch with its own attributed commit, merged back with
	// --no-ff so the merge commit is real rather than a fast-forward.
	run("checkout", "-q", "-b", "side")
	run("commit", "--allow-empty", "-q", "-m", "side work"+trailer)
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "Merge pull request #1 from side", "side")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	// The merge commit carries no trailer and must not be reported.
	if err := runCheck(cmd, "base"); err != nil {
		t.Errorf("runCheck failed on a merge commit: %v\n%s", err, buf.String())
	}
}
