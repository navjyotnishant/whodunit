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
