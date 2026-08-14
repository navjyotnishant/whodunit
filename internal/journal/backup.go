// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Daily rotating copies of the journal, taken on push.

package journal

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BackupGenerations is the default number of daily copies kept, used when a
// caller does not say. The configured value lives in config.BackupDays.
const BackupGenerations = 7

// backupInterval is the minimum age of the newest backup before another is
// taken.
//
// Backups are triggered on push, and a push happens many times a day. Copying
// a growing database on each one would spend more time on backups than on
// the work being backed up, so the trigger is "a push happened" and the rate
// limit is time.
const backupInterval = 24 * time.Hour

// BackupDir is where copies live, given the whodunit home.
//
// Beside the live database rather than inside data/, so a backup is never
// mistaken for something the tool reads. Nothing in whodunit opens these
// files; they exist to be copied back by hand.
func BackupDir(home string) string { return filepath.Join(home, "backups") }

func backupPath(home string, n int) string {
	return filepath.Join(BackupDir(home), fmt.Sprintf("journal_%d.db.gz", n))
}

// Backup copies the live database if the newest copy is older than a day.
//
// The live database is never touched — not rotated, not truncated, not
// vacuumed here. A backup that modifies the thing it is backing up is a
// restore risk rather than a safety net.
//
// Returns false when a copy was not due, which is the common case: this runs
// on every push and takes a copy on roughly one of them per day.
func Backup(home, dataDir string, generations int) (taken bool, err error) {
	if generations <= 0 {
		// Explicitly disabled. Existing copies are left alone: deleting
		// someone's backups because they turned off future ones would be
		// a surprise, and they can remove the directory themselves.
		return false, nil
	}
	live := DBPath(dataDir)
	if _, err := os.Stat(live); os.IsNotExist(err) {
		return false, nil
	}

	if fresh, err := backupIsFresh(home); err != nil || fresh {
		return false, err
	}
	if err := os.MkdirAll(BackupDir(home), 0o700); err != nil {
		return false, err
	}

	// Shift the generations before writing, oldest first so nothing
	// overwrites a copy that has not moved yet. The last is removed rather
	// than renamed, which is what bounds the set.
	_ = os.Remove(backupPath(home, generations))
	for i := generations - 1; i >= 1; i-- {
		_ = os.Rename(backupPath(home, i), backupPath(home, i+1))
	}

	// Written to a temporary name and renamed into place, so an interrupted
	// backup cannot leave a truncated file looking like generation 1.
	tmp := backupPath(home, 1) + ".partial"
	if err := copyGzip(live, tmp); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, backupPath(home, 1)); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// backupIsFresh reports whether the newest backup is under a day old.
func backupIsFresh(home string) (bool, error) {
	info, err := os.Stat(backupPath(home, 1))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return time.Since(info.ModTime()) < backupInterval, nil
}

// copyGzip writes a gzip-compressed copy of src at dst.
//
// SQLite files compress well — they are mostly text paths and repeated
// hashes — so a week of copies costs a fraction of one live database.
func copyGzip(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	// Flush to disk before the caller renames this into place: a rename is
	// atomic, but only over bytes that actually reached the filesystem.
	return out.Sync()
}

// BackupStatus is what `dun status` and `dun verify` report.
type BackupStatus struct {
	Count  int
	Newest time.Time
	Bytes  int64
}

// Backups reports what copies exist.
func Backups(home string, generations int) BackupStatus {
	var st BackupStatus
	if generations <= 0 {
		generations = BackupGenerations
	}
	for i := 1; i <= generations; i++ {
		info, err := os.Stat(backupPath(home, i))
		if err != nil {
			continue
		}
		st.Count++
		st.Bytes += info.Size()
		if info.ModTime().After(st.Newest) {
			st.Newest = info.ModTime()
		}
	}
	return st
}
