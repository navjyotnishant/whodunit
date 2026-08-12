package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
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
	return dir
}

func TestRunCommitMsgAcceptsValidTrailer(t *testing.T) {
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "msg.txt")
	content := "feat: thing\n\nAI-Attribution: status=undetermined; method=undetermined\n"
	if err := os.WriteFile(msgFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	if err := runCommitMsg([]string{msgFile}); err != nil {
		t.Errorf("runCommitMsg() = %v, want nil for a valid trailer", err)
	}
}

func TestRunCommitMsgRejectsMalformedTrailer(t *testing.T) {
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "msg.txt")
	content := "feat: thing\n\nAI-Attribution: status=bogus; method=undetermined\n"
	if err := os.WriteFile(msgFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	if err := runCommitMsg([]string{msgFile}); err == nil {
		t.Error("runCommitMsg() = nil, want error for malformed trailer")
	}
}

func TestRunCommitMsgRejectsMultipleTrailers(t *testing.T) {
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "msg.txt")
	content := "feat: thing\n\n" +
		"AI-Attribution: status=undetermined; method=undetermined\n" +
		"AI-Attribution: status=undetermined; method=undetermined\n"
	if err := os.WriteFile(msgFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	if err := runCommitMsg([]string{msgFile}); err == nil {
		t.Error("runCommitMsg() = nil, want error for duplicate trailers")
	}
}

func TestRunCommitMsgAllowsMissingTrailer(t *testing.T) {
	// Not this hook's concern — prepare-commit-msg is what stamps it.
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(msgFile, []byte("feat: thing\n"), 0o644); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	if err := runCommitMsg([]string{msgFile}); err != nil {
		t.Errorf("runCommitMsg() = %v, want nil for a missing trailer", err)
	}
}

func TestRunCommitMsgNoArgsIsNoop(t *testing.T) {
	if err := runCommitMsg(nil); err != nil {
		t.Errorf("runCommitMsg(nil) = %v, want nil", err)
	}
}

func TestStagedFilesInRealRepo(t *testing.T) {
	dir := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	cmd := exec.Command("git", "add", "a.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	files, err := stagedFiles()
	if err != nil {
		t.Fatalf("stagedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "a.go" {
		t.Errorf("stagedFiles() = %v, want [a.go]", files)
	}
}

func TestStagedFilesNothingStaged(t *testing.T) {
	dir := initTestRepo(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	files, err := stagedFiles()
	if err != nil {
		t.Fatalf("stagedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("stagedFiles() = %v, want empty", files)
	}
}

func TestDetermineTrailerDegradesToUndeterminedWithNothingStaged(t *testing.T) {
	dir := initTestRepo(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	trailer := determineTrailer()
	if !strings.Contains(trailer.Format(), "status=undetermined") {
		t.Errorf("determineTrailer() = %q, want undetermined with nothing staged", trailer.Format())
	}
}

func TestRunPrepareCommitMsgAppendsTrailer(t *testing.T) {
	dir := initTestRepo(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	msgFile := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	if err := runPrepareCommitMsg([]string{msgFile}); err != nil {
		t.Fatalf("runPrepareCommitMsg: %v", err)
	}

	got, err := os.ReadFile(msgFile)
	if err != nil {
		t.Fatalf("read msg file: %v", err)
	}
	if !strings.Contains(string(got), "AI-Attribution:") {
		t.Errorf("commit message missing trailer after runPrepareCommitMsg: %q", got)
	}
}

func TestRunPrepareCommitMsgNoArgsIsNoop(t *testing.T) {
	if err := runPrepareCommitMsg(nil); err != nil {
		t.Errorf("runPrepareCommitMsg(nil) = %v, want nil", err)
	}
}

func TestRunPrepareCommitMsgNeverFailsOnMissingFile(t *testing.T) {
	// Per spec: stamping errors must never fail the commit.
	if err := runPrepareCommitMsg([]string{"/does/not/exist/msg.txt"}); err != nil {
		t.Errorf("runPrepareCommitMsg() = %v, want nil even when the msg file can't be opened", err)
	}
}
