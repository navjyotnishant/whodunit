// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Daily backups rotate, bound, and never touch the live file.

package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedJournal(t *testing.T, dir string) {
	t.Helper()
	w, err := NewWriter(dir, "r")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(Entry{Timestamp: time.Now(), Agent: "claude-code",
		Session: "s", Event: "tool_use", Tool: "Edit", File: "/f.go"}); err != nil {
		t.Fatal(err)
	}
}

// The rate limit is the whole design: this runs on every push, and a push
// happens many times a day.
func TestBackupIsTakenOncePerDay(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	taken, err := Backup(home, data, BackupGenerations)
	if err != nil {
		t.Fatal(err)
	}
	if !taken {
		t.Fatal("no backup taken when none existed")
	}

	// A second push minutes later must not copy again.
	taken, err = Backup(home, data, BackupGenerations)
	if err != nil {
		t.Fatal(err)
	}
	if taken {
		t.Error("a second backup was taken the same day")
	}
}

func TestBackupsRotateAndAreBounded(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	// Age the newest copy past the interval each time round, which is what
	// a day passing looks like to Backup.
	for i := 0; i < BackupGenerations+3; i++ {
		if _, err := Backup(home, data, BackupGenerations); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		_ = os.Chtimes(backupPath(home, 1), old, old)
	}

	files, _ := filepath.Glob(filepath.Join(BackupDir(home), "journal_*.db.gz"))
	if len(files) != BackupGenerations {
		t.Errorf("kept %d backups, want %d: %v", len(files), BackupGenerations, files)
	}
}

// A backup that modifies what it is backing up is a restore risk rather
// than a safety net.
func TestBackupLeavesTheLiveDatabaseAlone(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	before, err := os.Stat(DBPath(data))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Backup(home, data, BackupGenerations); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(DBPath(data))
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("the live database changed: %d bytes at %v became %d at %v",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
}

// An interrupted copy must not leave a truncated file looking like the
// newest generation — that is the copy someone would restore from.
func TestAPartialBackupIsNeverTheNewestGeneration(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	if _, err := Backup(home, data, BackupGenerations); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupPath(home, 1) + ".partial"); !os.IsNotExist(err) {
		t.Error("a .partial file survived a successful backup")
	}
}

func TestBackupOfAnAbsentJournalIsNotAnError(t *testing.T) {
	home := t.TempDir()
	taken, err := Backup(home, filepath.Join(home, "data"), BackupGenerations)
	if err != nil {
		t.Errorf("backing up a journal that does not exist errored: %v", err)
	}
	if taken {
		t.Error("reported taking a backup of a database that is not there")
	}
}

// The configured count has to change behaviour, or the setting is
// decorative. Three generations means the fourth push drops the oldest.
func TestBackupHonoursTheConfiguredCount(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	const keep = 3
	for i := 0; i < keep+2; i++ {
		if _, err := Backup(home, data, keep); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		_ = os.Chtimes(backupPath(home, 1), old, old)
	}

	files, _ := filepath.Glob(filepath.Join(BackupDir(home), "journal_*.db.gz"))
	if len(files) != keep {
		t.Errorf("kept %d backups, want the configured %d: %v", len(files), keep, files)
	}
}

// Zero means "do not take any", and must not mean "take the default".
func TestBackupCountOfZeroTakesNone(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	seedJournal(t, data)

	taken, err := Backup(home, data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if taken {
		t.Error("took a backup when backups are disabled")
	}
	if _, err := os.Stat(BackupDir(home)); !os.IsNotExist(err) {
		t.Error("created a backup directory when backups are disabled")
	}
}
