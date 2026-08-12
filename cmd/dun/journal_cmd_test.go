package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

func chdirToTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
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

	dir, err := journalDir()
	if err != nil {
		t.Fatalf("journalDir: %v", err)
	}
	w, err := journal.NewWriter(dir)
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

func TestJournalPurgeRemovesJournal(t *testing.T) {
	chdirToTestRepo(t)

	dir, err := journalDir()
	if err != nil {
		t.Fatalf("journalDir: %v", err)
	}
	w, err := journal.NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Append(journal.Entry{Timestamp: time.Now(), Agent: "claude-code", Event: "tool_use", File: "main.go"})
	w.Close()

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "purge"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("journal purge: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("journal dir still exists after purge: err=%v", err)
	}
}

func TestJournalPurgeOnNonexistentJournal(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"journal", "purge"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("journal purge on nonexistent journal = %v, want nil (os.RemoveAll is a no-op)", err)
	}
}
