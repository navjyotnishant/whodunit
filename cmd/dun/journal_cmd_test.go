package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// chdirToTestRepo creates an isolated git repo with one commit, chdirs into
// it, and points WHODUNIT_HOME at a temp dir so tests never read or write
// the real global store.
//
// The commit matters: a repository's identifier is its root commit SHA, so
// a repo with no commits has no identity and journal operations correctly
// refuse to run.
func chdirToTestRepo(t *testing.T) string {
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
	if err := os.WriteFile(filepath.Join(dir, ".seed"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	run("add", ".seed")
	run("commit", "-q", "-m", "chore: seed")

	t.Setenv("WHODUNIT_HOME", t.TempDir())

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return dir
}

func TestJournalShowOnEmptyJournal(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "show"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("journal show on empty journal = %v, want nil", err)
	}
	if buf.String() != "" {
		t.Errorf("journal show on empty journal = %q, want empty output", buf.String())
	}
}

func TestJournalShowPrintsEntries(t *testing.T) {
	chdirToTestRepo(t)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatalf("journalDataDir: %v", err)
	}
	repoID, err := currentRepoID()
	if err != nil {
		t.Fatalf("currentRepoID: %v", err)
	}
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Append(journal.Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "main.go"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	w.Close()

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "show"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("journal show: %v", err)
	}
	if !strings.Contains(buf.String(), "claude-code") {
		t.Errorf("journal show output = %q, want it to contain the entry", buf.String())
	}
}

func TestJournalShowIsScopedToThisRepo(t *testing.T) {
	chdirToTestRepo(t)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatalf("journalDataDir: %v", err)
	}

	// An entry belonging to a different repository must not appear.
	other, err := journal.NewWriter(dataDir, "some-other-repo-root-sha")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := other.Append(journal.Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "SHOULD_NOT_APPEAR.go"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	other.Close()

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "show"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("journal show: %v", err)
	}
	if strings.Contains(buf.String(), "SHOULD_NOT_APPEAR") {
		t.Errorf("journal show leaked another repository's entry: %s", buf.String())
	}
}

func TestJournalPurgeRemovesOnlyThisRepo(t *testing.T) {
	chdirToTestRepo(t)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatalf("journalDataDir: %v", err)
	}
	repoID, err := currentRepoID()
	if err != nil {
		t.Fatalf("currentRepoID: %v", err)
	}

	mine, _ := journal.NewWriter(dataDir, repoID)
	mine.Append(journal.Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "mine.go"})
	mine.Close()

	theirs, _ := journal.NewWriter(dataDir, "other-repo")
	theirs.Append(journal.Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "theirs.go"})
	theirs.Close()

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "purge"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("journal purge: %v", err)
	}

	got, _ := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if len(got) != 0 {
		t.Errorf("this repo should be empty after purge, got %d entries", len(got))
	}

	// Purging one repo in a shared store must never take another with it.
	survived, _ := journal.ReadRange(dataDir, "other-repo", time.Time{}, time.Time{})
	if len(survived) != 1 {
		t.Errorf("purge destroyed another repository's entries: %d survived, want 1", len(survived))
	}
}

func TestJournalPurgeOnNonexistentJournal(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "purge"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("journal purge on nonexistent journal = %v, want nil", err)
	}
}
