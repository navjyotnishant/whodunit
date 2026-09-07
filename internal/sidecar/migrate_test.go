// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: An existing install gets migrated; a migrated one is left alone.

package sidecar

import (
	"path/filepath"
	"testing"
	"time"
)

// oldSchemaStore is a database in the shape a pre-WHO-176 install has:
// whodunit_repos keyed on repo_id alone, with rows in it.
//
// Built by hand rather than by checking out the old schema, because the
// point is to reproduce what is on someone's disk today, and that shape is
// fixed regardless of what the current source says.
func oldSchemaStore(t *testing.T, rows int) *Store {
	t.Helper()
	db, err := Open("sqlite:///" + filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE whodunit_repos (
		repo_id      VARCHAR(64)  NOT NULL,
		contributor  VARCHAR(320) NOT NULL DEFAULT '',
		spec_version VARCHAR(16)  NOT NULL DEFAULT '',
		synced_at    BIGINT       NOT NULL,
		PRIMARY KEY (repo_id))`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec(
			`INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
			 VALUES (?, ?, ?, ?)`,
			"repo"+string(rune('a'+i)), "dev@example.com", "0.2", int64(i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

// The story's point: an install that already has data gets the new key.
func TestAnExistingInstallIsMigrated(t *testing.T) {
	db := oldSchemaStore(t, 3)

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	key, err := sqlitePrimaryKey(db, "whodunit_repos")
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 2 {
		t.Fatalf("primary key is %v after migrating, want (repo_id, contributor)", key)
	}

	// Every row intact. A migration that produces the right shape and
	// loses rows has failed at the only thing that mattered.
	n, err := countRows(db, "whodunit_repos")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("got %d row(s) after migrating, want 3", n)
	}

	// And the new key does what it was widened for.
	if _, err := db.Exec(
		`INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
		 VALUES (?, ?, ?, ?)`, "repoa", "second@example.com", "0.2", 99); err != nil {
		t.Errorf("the migrated table still rejects a second contributor: %v", err)
	}
}

// Running it twice must migrate once.
func TestMigratingTwiceMigratesOnce(t *testing.T) {
	db := oldSchemaStore(t, 2)

	first, err := Migrate(db, time.Now())
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first run applied %v, want one version", first)
	}

	second, err := Migrate(db, time.Now())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want nothing", second)
	}

	n, err := countRows(db, "whodunit_repos")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("got %d row(s) after migrating twice, want 2", n)
	}
}

// A fresh database is already the right shape, so it records the version
// without rebuilding anything.
//
// The distinction matters: a needless destructive step is still a
// destructive step, and a fresh install has no backup worth the name.
func TestAFreshDatabaseIsNotRebuilt(t *testing.T) {
	db := openStore(t) // EnsureSchema already ran, in the current shape

	if v := appliedVersion(db); v != SchemaVersion {
		t.Errorf("fresh database records version %d, want %d", v, SchemaVersion)
	}

	// No backup table, because no rebuild happened.
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type='table' AND name LIKE 'whodunit_repos_backup_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a fresh database produced %d backup table(s); it was rebuilt "+
			"when it did not need to be", n)
	}
}

// A failed rebuild must not record the version it did not reach.
//
// Otherwise one failure becomes a permanent skip: the database claims to
// be migrated while holding the old key, and the collision comes back with
// nothing left to notice it.
func TestAFailedMigrationDoesNotRecordItsVersion(t *testing.T) {
	db := oldSchemaStore(t, 1)

	// A backup table already sitting in the way makes BackupTable fail,
	// which stops the rebuild before anything destructive happens — the
	// same shape as a disk-full or permissions failure.
	now := time.Now()
	blocker := "whodunit_repos" + backupSuffix(now)
	if _, err := db.Exec("CREATE TABLE " + blocker + " (x INTEGER)"); err != nil {
		t.Fatal(err)
	}

	if _, err := Migrate(db, now); err == nil {
		t.Fatal("Migrate reported success with the backup blocked")
	}

	if v := appliedVersion(db); v != firstVersion {
		t.Errorf("a failed migration recorded version %d; the next run would "+
			"skip a migration that never happened", v)
	}

	// The original is untouched, and the next run can still try.
	n, err := countRows(db, "whodunit_repos")
	if err != nil {
		t.Fatalf("original unreadable after a failed migration: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d row(s), want 1", n)
	}
}

// A database with no recorded version is old, not new.
func TestNoRecordedVersionMeansTheFirstOne(t *testing.T) {
	db := oldSchemaStore(t, 1)
	if v := appliedVersion(db); v != firstVersion {
		t.Errorf("a database with no whodunit_schema table reports version %d, "+
			"want %d — treating it as current would skip every migration", v, firstVersion)
	}
}
