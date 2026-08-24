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

	trailer := determineTrailer("")
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

// An agent that leaves no local transcript still declares itself in the
// commit message, and that is the only evidence such a commit carries.
// Before this path existed, a whole Copilot team's every commit came back
// undetermined - correct, and useless.
func TestPrepareCommitMsgStampsADeclaration(t *testing.T) {
	dir := t.TempDir()
	msg := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msg,
		[]byte("Fix the thing\n\nCo-authored-by: Copilot <copilot@github.com>\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPrepareCommitMsg([]string{msg}); err != nil {
		t.Fatalf("runPrepareCommitMsg: %v", err)
	}
	out, err := os.ReadFile(msg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "method=declared") {
		t.Errorf("declaration not stamped: %s", got)
	}
	if !strings.Contains(got, "agent=copilot") {
		t.Errorf("agent not carried: %s", got)
	}
	// The weakest rung says an agent was involved, not how much of the
	// commit it wrote. A ratio here would be invented (NAV-21).
	if strings.Contains(got, "ratio=") {
		t.Errorf("a declaration must carry no ratio: %s", got)
	}
}

// The trailer this reads is a decade-old convention written by people
// about people. Matching it without checking who it names would attribute
// an enormous amount of ordinary collaboration to an agent.
func TestPrepareCommitMsgIgnoresAHumanCoAuthor(t *testing.T) {
	dir := t.TempDir()
	msg := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msg,
		[]byte("Fix the thing\n\nCo-authored-by: Alice <alice@example.com>\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPrepareCommitMsg([]string{msg}); err != nil {
		t.Fatalf("runPrepareCommitMsg: %v", err)
	}
	out, _ := os.ReadFile(msg)
	if strings.Contains(string(out), "status=assisted") {
		t.Errorf("a human co-author is not an agent: %s", out)
	}
}
