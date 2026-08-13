// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The hook log — bounded, per-repo purge, and never fatal.

package hooklog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenRead(t *testing.T) {
	home := t.TempDir()

	Write(home, Entry{Hook: "prepare-commit-msg", Event: "determine", Detail: "first"})
	Write(home, Entry{Hook: "pre-push", Event: "sync", Detail: "second"})

	entries, err := Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// Newest first: the question a log answers is nearly always "what just
	// happened".
	if entries[0].Detail != "second" {
		t.Errorf("entries are not newest-first: got %q first", entries[0].Detail)
	}
}

// NAV-75 criterion 7. A log that can fail a commit is worse than no log, so
// every write path has to survive a home it cannot write to.
func TestWritingNeverPanicsOrBlocks(t *testing.T) {
	// A file where the directory should be: MkDirAll fails, and every
	// subsequent step must give up quietly rather than panic.
	home := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(home, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The assertion is that this returns at all.
	Write(home, Entry{Hook: "prepare-commit-msg", Event: "determine"})

	Write("", Entry{Hook: "prepare-commit-msg"})
}

// Criterion 9: bounded. An instrumented machine runs these hooks on every
// commit forever, so an unbounded file is a slow leak nobody notices until
// a disk fills.
func TestTheLogIsBounded(t *testing.T) {
	home := t.TempDir()

	// Each entry is a few hundred bytes; enough of them to pass maxBytes
	// twice over, which also exercises the second rotation.
	big := strings.Repeat("x", 2000)
	for i := 0; i < 1500; i++ {
		Write(home, Entry{Hook: "prepare-commit-msg", Event: "determine", Detail: big})
	}

	// The ceiling is every generation at maxBytes, plus whatever was
	// written into the live file since the last rotation.
	limit := int64(maxBytes) * int64(maxGenerations+2)
	if size := Size(home); size > limit {
		t.Fatalf("the log grew to %d bytes, beyond the %d bound", size, limit)
	}
	if size := Size(home); size == 0 {
		t.Fatal("rotation removed everything; the recent past must survive")
	}
}

// Rotation must not lose the recent past at the moment someone goes
// looking, which is the whole reason a previous generation is kept.
func TestRotationKeepsThePreviousGeneration(t *testing.T) {
	home := t.TempDir()

	big := strings.Repeat("y", 2000)
	for i := 0; i < 800; i++ {
		Write(home, Entry{Hook: "pre-push", Event: "sync", Detail: big})
	}
	if _, err := os.Stat(genPath(home, 1)); err != nil {
		t.Fatalf("no previous generation was kept: %v", err)
	}

	entries, err := Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 800 {
		t.Fatalf("Read returned %d entries; both generations should be read", len(entries))
	}
}

// Purge is scoped to a repository, but the log is global. Deleting the file
// would erase other projects' records as a side effect of purging one.
func TestPurgeRemovesOnlyTheNamedRepository(t *testing.T) {
	home := t.TempDir()

	Write(home, Entry{RepoID: "aaa", Hook: "prepare-commit-msg", Detail: "keep me"})
	Write(home, Entry{RepoID: "bbb", Hook: "prepare-commit-msg", Detail: "purge me"})
	Write(home, Entry{RepoID: "aaa", Hook: "pre-push", Detail: "keep me too"})

	removed, err := PurgeRepo(home, "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d entries, want 1", removed)
	}

	entries, err := Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries after purge, want 2", len(entries))
	}
	for _, e := range entries {
		if e.RepoID == "bbb" {
			t.Error("a purged repository's entry survived")
		}
	}
}

func TestPurgeOfAnAbsentRepositoryChangesNothing(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{RepoID: "aaa", Hook: "pre-push", Detail: "untouched"})

	removed, err := PurgeRepo(home, "nothing-here")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d entries from a repository with none", removed)
	}
	entries, _ := Read(home, 0)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the original 1", len(entries))
	}
}

// A crash can leave a half-written final line. Losing the whole log to one
// bad line would destroy exactly the history someone is trying to read.
func TestACorruptLineDoesNotKillTheRead(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{Hook: "pre-push", Detail: "good"})

	f, err := os.OpenFile(path(home), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"time":"truncated mid-w`)
	f.Close()

	entries, err := Read(home, 0)
	if err != nil {
		t.Fatalf("a corrupt line failed the whole read: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the 1 good one", len(entries))
	}
}

func TestReadOfAnAbsentLogIsNotAnError(t *testing.T) {
	entries, err := Read(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("reading an absent log errored: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries from an absent log", len(entries))
	}
}

// The log records which repositories were worked on and when, which is not
// something to leave readable by every account on a shared machine.
func TestTheLogIsOwnerOnly(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{Hook: "pre-push", Detail: "x"})

	info, err := os.Stat(path(home))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the log is mode %04o, want 0600", mode)
	}
}

func TestLimitTakesTheNewest(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 10; i++ {
		Write(home, Entry{
			Time:   time.Now().Add(time.Duration(i) * time.Second),
			Hook:   "pre-push",
			Detail: string(rune('a' + i)),
		})
	}

	entries, err := Read(home, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Detail != "j" {
		t.Errorf("limit dropped the newest entries: got %q first", entries[0].Detail)
	}
}
