package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Messages git pre-fills from an earlier commit already carry a trailer.
// These cases drive runPrepareCommitMsg with exactly the arguments git
// passes it, then check the result is something commit-msg accepts: before
// WHO-261 an amend got a second trailer and every amend was rejected.

const (
	strongTrailer = "AI-Attribution: v=2; status=assisted; method=intersected; agent=claude-code"
	weakTrailer   = "AI-Attribution: v=2; status=undetermined; method=undetermined"
)

// repoWithTwoCommits returns a repository with two commits and the working
// directory set to it, plus the sha of each commit.
func repoWithTwoCommits(t *testing.T) (dir, first, second string) {
	t.Helper()
	dir = initTestRepo(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for i, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-q", "--no-verify", "-m", "commit "+name)
		sha := git("rev-parse", "HEAD")
		if i == 0 {
			first = sha
		} else {
			second = sha
		}
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir, first, second
}

// prepare writes msg, runs prepare-commit-msg with git's arguments, and
// returns the resulting message after checking commit-msg accepts it.
func prepare(t *testing.T, dir, msg string, gitArgs ...string) string {
	t.Helper()
	msgFile := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPrepareCommitMsg(append([]string{msgFile}, gitArgs...)); err != nil {
		t.Fatalf("runPrepareCommitMsg: %v", err)
	}
	got, err := os.ReadFile(msgFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommitMsg([]string{msgFile}); err != nil {
		t.Fatalf("commit-msg rejected the prepared message: %v\n%s", err, got)
	}
	return string(got)
}

func trailerCount(msg string) int { return len(trailerLines(msg)) }

func TestAmendKeepsTheExistingTrailer(t *testing.T) {
	dir, _, head := repoWithTwoCommits(t)
	// --amend and --amend --no-edit both arrive as "commit <HEAD>".
	got := prepare(t, dir, "commit b.txt\n\n"+strongTrailer+"\n", "commit", head)

	if n := trailerCount(got); n != 1 {
		t.Fatalf("got %d trailers, want 1:\n%s", n, got)
	}
	// Kept, not recomputed: nothing new is staged, so a recomputation would
	// have replaced real evidence with undetermined.
	if !strings.Contains(got, strongTrailer) {
		t.Errorf("amend replaced the original trailer:\n%s", got)
	}
}

func TestReusingAnotherCommitsMessageRecomputes(t *testing.T) {
	dir, first, _ := repoWithTwoCommits(t)
	// git commit -C <first>: the reused trailer describes that commit's
	// change, not this one.
	got := prepare(t, dir, "commit a.txt\n\n"+strongTrailer+"\n", "commit", first)

	if n := trailerCount(got); n != 1 {
		t.Fatalf("got %d trailers, want 1:\n%s", n, got)
	}
	if strings.Contains(got, "method=intersected") {
		t.Errorf("kept a trailer that belongs to another commit:\n%s", got)
	}
}

func TestSeveralTrailersCollapseToTheStrongest(t *testing.T) {
	dir, _, _ := repoWithTwoCommits(t)
	// A rebase squash joins the messages of the commits it combines.
	msg := "squashed\n\n" + weakTrailer + "\n\nsecond\n\n" + strongTrailer + "\n"
	got := prepare(t, dir, msg, "squash")

	if n := trailerCount(got); n != 1 {
		t.Fatalf("got %d trailers, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "method=intersected") {
		t.Errorf("did not keep the strongest trailer:\n%s", got)
	}
}

func TestAMessageWithoutATrailerStillGetsOne(t *testing.T) {
	dir, _, _ := repoWithTwoCommits(t)
	got := prepare(t, dir, "plain message\n", "message")

	if n := trailerCount(got); n != 1 {
		t.Fatalf("got %d trailers, want 1:\n%s", n, got)
	}
}

func TestNothingStagedIsStampedWithTheCurrentVersion(t *testing.T) {
	repoWithTwoCommits(t)
	// WHO-262: this path used to emit v=1 while every other commit says v=2.
	got := determineTrailer("").Format()
	want := fmt.Sprintf("AI-Attribution: v=%d;", spec.Version)
	if !strings.HasPrefix(got, want) {
		t.Errorf("determineTrailer with nothing staged = %q, want prefix %q", got, want)
	}
}
