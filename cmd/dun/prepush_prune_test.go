// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: The only path that deletes recorded data.

package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// afterSuccessfulSync is the one path in this project that removes recorded
// data, and it had no test at all.
//
// Its safety rests on two orderings that nothing verified: a backup is taken
// before the prune, so the copy still holds what the prune is about to
// remove; and the prune runs only after a sync succeeded, so anything
// deleted already exists somewhere else. Both are stated in comments. This
// asserts them.

// seedAgedHashes writes line hashes at a given age, returning how many.
func seedAgedHashes(t *testing.T, dataDir, repoID string, age time.Duration, n int) {
	t.Helper()
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ts := time.Now().Add(-age)
	hashes := make([]uint64, n)
	for i := range hashes {
		// Distinct per age band so the two groups cannot collide.
		hashes[i] = uint64(age.Hours())<<32 | uint64(i)
	}
	if err := w.AppendLines(hashes, ts); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(journal.Entry{
		Timestamp: ts, Agent: "claude-code", Session: "s",
		Event: "tool_use", Tool: "Edit", File: "/repo/main.go",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterSuccessfulSyncPrunesOnlyBeyondRetention(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	const repoID = "prune-repo"

	// Well inside the retention window, and well beyond it.
	seedAgedHashes(t, dataDir, repoID, 24*time.Hour, 10)
	seedAgedHashes(t, dataDir, repoID, 200*24*time.Hour, 10)

	before, err := journal.ReadLineHashes(dataDir, repoID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 20 {
		t.Fatalf("seeded %d hashes, want 20", len(before))
	}

	afterSuccessfulSync(hookPrePush)

	after, err := journal.ReadLineHashes(dataDir, repoID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 10 {
		t.Errorf("after pruning there are %d hashes, want 10: the old ones "+
			"should go and the recent ones must stay", len(after))
	}

	// The recent ones specifically, not just "ten of something".
	recent, err := journal.ReadLineHashes(dataDir, repoID, time.Now().Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 10 {
		t.Errorf("%d recent hashes survived, want 10 — the prune took hashes "+
			"inside the retention window, which is evidence the commit hook "+
			"still queries", len(recent))
	}
}

// Entries are never pruned, only line hashes.
//
// The retention design rests on this: entries are what the report's history
// and after-the-fact verification are built from, so pruning them would make
// "you started using AI on 14 July" a claim that moves every night.
func TestAfterSuccessfulSyncNeverPrunesEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	const repoID = "entries-repo"

	seedAgedHashes(t, dataDir, repoID, 400*24*time.Hour, 5)

	before, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	afterSuccessfulSync(hookPrePush)

	after, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("entries went from %d to %d. Only line hashes may be pruned: "+
			"entries are what the report's history is built from, and an old "+
			"commit must stay verifiable after the fact.", len(before), len(after))
	}
}

// The backup must be taken before the prune, so it still holds what the
// prune removes. Taking it afterwards would bake the deletion into the only
// local copy of the history.
func TestBackupHappensBeforeThePrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	const repoID = "order-repo"

	// Only old hashes, so the prune definitely removes something.
	seedAgedHashes(t, dataDir, repoID, 400*24*time.Hour, 20)

	afterSuccessfulSync(hookPrePush)

	// The prune ran.
	left, err := journal.ReadLineHashes(dataDir, repoID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d hashes survived a prune that should have taken all of "+
			"them; this test proves nothing about ordering unless the prune "+
			"actually ran", len(left))
	}

	// And a backup exists, holding the pre-prune state.
	st := journal.Backups(home, config.Config{}.BackupDays)
	if st.Count == 0 {
		t.Fatal("no backup was written before the prune; the deletion is now " +
			"the only local history there is")
	}
	if st.Bytes == 0 {
		t.Error("the backup is empty")
	}
}

// A backup that is never restored is not a backup. The generations are
// gzipped copies of the live file, so at minimum they must decompress into
// something SQLite opens.
func TestABackupCanBeOpenedAgain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	const repoID = "restore-repo"
	seedAgedHashes(t, dataDir, repoID, 24*time.Hour, 12)

	if _, err := journal.Backup(home, dataDir, 7); err != nil {
		t.Fatal(err)
	}

	gz := filepath.Join(journal.BackupDir(home), "journal_1.db.gz")
	if _, err := os.Stat(gz); err != nil {
		t.Fatalf("no backup generation was written: %v", err)
	}

	// Restore by hand, the way a user would, into a fresh data directory.
	restored := t.TempDir()
	if err := gunzipTo(t, gz, journal.DBPath(restored)); err != nil {
		t.Fatal(err)
	}

	hashes, err := journal.ReadLineHashes(restored, repoID, time.Time{})
	if err != nil {
		t.Fatalf("the restored copy is not a readable journal: %v", err)
	}
	if len(hashes) != 12 {
		t.Errorf("the restored journal holds %d hashes, want 12", len(hashes))
	}
}

// gunzipTo decompresses src to dst, which is what restoring a backup means.
func gunzipTo(t *testing.T, src, dst string) error {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, zr)
	return err
}
