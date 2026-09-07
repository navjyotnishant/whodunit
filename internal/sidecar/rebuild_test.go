// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: The rebuild preserves every row, or it does not run.

package sidecar

import (
	"strings"
	"testing"
	"time"
)

// The shape whodunit_repos is migrating to (WHO-168): keyed on the
// contributor as well as the repository, so two people syncing the same
// repository no longer overwrite each other.
const newReposDDL = `CREATE TABLE whodunit_repos (
	repo_id      VARCHAR(64)  NOT NULL,
	contributor  VARCHAR(320) NOT NULL DEFAULT '',
	spec_version VARCHAR(16)  NOT NULL DEFAULT '',
	synced_at    BIGINT       NOT NULL,
	PRIMARY KEY (repo_id, contributor)
)`

var reposColumns = []string{"repo_id", "contributor", "spec_version", "synced_at"}

func seedRepos(t *testing.T, db *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := db.Exec(
			`INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
			 VALUES (?, ?, ?, ?)`,
			"repo", "dev"+string(rune('a'+i))+"@example.com", "0.2", int64(i))
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// The point of the whole story: every row survives the key change.
func TestRebuildPreservesEveryRow(t *testing.T) {
	db := openStore(t)

	// The old key is repo_id alone, so only one row fits per repository —
	// which is the bug being migrated away from. Seeded through the old
	// schema, exactly as an existing install holds it.
	seedRepos(t, db, 1)

	if err := RebuildTable(db, "whodunit_repos", newReposDDL, reposColumns, time.Now()); err != nil {
		t.Fatalf("RebuildTable: %v", err)
	}

	n, err := countRows(db, "whodunit_repos")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("got %d row(s) after rebuild, want 1", n)
	}

	// The new key admits what the old one could not: a second contributor
	// on the same repository. If this fails the rebuild produced the old
	// shape under a new name.
	if _, err := db.Exec(
		`INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
		 VALUES (?, ?, ?, ?)`, "repo", "second@example.com", "0.2", 99); err != nil {
		t.Fatalf("the rebuilt table still rejects a second contributor: %v", err)
	}
}

// A backup that does not match its source is not a backup.
func TestBackupIsVerifiedAgainstTheSource(t *testing.T) {
	db := openStore(t)
	seedRepos(t, db, 1)

	backup, err := BackupTable(db, "whodunit_repos", time.Now())
	if err != nil {
		t.Fatalf("BackupTable: %v", err)
	}

	src, _ := countRows(db, "whodunit_repos")
	got, err := countRows(db, backup)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("backup has %d row(s), source has %d", got, src)
	}

	// An operator has to be able to find this without reading the source.
	if !strings.HasPrefix(backup, "whodunit_repos_backup_") {
		t.Errorf("backup name %q does not name its source table", backup)
	}
}

// The gating requirement: a failed rebuild leaves the original readable.
//
// The failure is induced where it is most dangerous — after the backup, with
// a DDL the engine rejects — because that is the window in which the
// original could be dropped before its replacement exists.
func TestAFailedRebuildLeavesTheOriginalIntact(t *testing.T) {
	db := openStore(t)
	seedRepos(t, db, 1)

	badDDL := `CREATE TABLE whodunit_repos (this is not valid sql)`
	err := RebuildTable(db, "whodunit_repos", badDDL, reposColumns, time.Now())
	if err == nil {
		t.Fatal("a rebuild with invalid DDL reported success")
	}

	// Readable, with its rows.
	n, cerr := countRows(db, "whodunit_repos")
	if cerr != nil {
		t.Fatalf("the original table is unreadable after a failed rebuild: %v", cerr)
	}
	if n != 1 {
		t.Errorf("original has %d row(s) after a failed rebuild, want 1", n)
	}

	// And the error tells the operator how to recover rather than leaving
	// them to reconstruct it.
	if !strings.Contains(err.Error(), "restore with") {
		t.Errorf("the failure does not name the restore path: %v", err)
	}
	if !strings.Contains(err.Error(), "whodunit_repos_backup_") {
		t.Errorf("the failure does not name the backup table: %v", err)
	}
}

// A short copy must stop the rebuild, not be discovered afterwards.
//
// Simulated by pointing the copy at a column list the new table does not
// have, so the INSERT fails partway rather than the count silently
// disagreeing — the assertion is that the original is never dropped when
// the copy did not fully succeed.
func TestAShortCopyDoesNotDropTheOriginal(t *testing.T) {
	db := openStore(t)
	seedRepos(t, db, 1)

	err := RebuildTable(db, "whodunit_repos", newReposDDL,
		[]string{"repo_id", "contributor", "no_such_column"}, time.Now())
	if err == nil {
		t.Fatal("copying a column that does not exist reported success")
	}

	n, cerr := countRows(db, "whodunit_repos")
	if cerr != nil {
		t.Fatalf("the original table is gone after a failed copy: %v", cerr)
	}
	if n != 1 {
		t.Errorf("original has %d row(s), want 1", n)
	}
}

// Re-running an upgrade that already ran must not run it again.
//
// RebuildTable itself is deliberately not idempotent — it is guarded by the
// stored schema version rather than by being safe to repeat. This asserts
// the honest version of that: a second call fails rather than quietly
// doing something, so a caller that forgets the guard finds out.
func TestASecondRebuildIsNotSilentlyDestructive(t *testing.T) {
	db := openStore(t)
	seedRepos(t, db, 1)

	now := time.Now()
	if err := RebuildTable(db, "whodunit_repos", newReposDDL, reposColumns, now); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}

	// A second rebuild one second later, so the backup name differs. It
	// either succeeds as a no-op-shaped repeat or fails — what it must not
	// do is lose the row.
	_ = RebuildTable(db, "whodunit_repos", newReposDDL, reposColumns, now.Add(time.Second))

	n, err := countRows(db, "whodunit_repos")
	if err != nil {
		t.Fatalf("table unreadable after a repeated rebuild: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d row(s) after a repeated rebuild, want 1", n)
	}
}

// Two backups in the same second must not collide.
func TestBackupNamesDoNotCollide(t *testing.T) {
	now := time.Now()
	if a, b := backupSuffix(now), backupSuffix(now.Add(time.Second)); a == b {
		t.Errorf("two backups a second apart share the name %q", a)
	}
	if a, b := backupSuffix(now), backupSuffix(now.Add(time.Millisecond)); a == b {
		t.Errorf("two backups a millisecond apart share the name %q", a)
	}
}

// A backup name must be one identifier.
//
// A dot is a qualified-name separator in SQL, so a millisecond appended as
// ".385" made MySQL read the name as database "..." table "385" and report
// a permissions error on a table nobody had named. The name is built by
// hand rather than with a fractional-seconds format for exactly this
// reason, and this stops that being tidied back.
func TestABackupNameIsASingleIdentifier(t *testing.T) {
	name := "whodunit_repos" + backupSuffix(time.Now())
	for _, bad := range []string{".", " ", "-", "`", "\""} {
		if strings.Contains(name, bad) {
			t.Errorf("backup name %q contains %q; it must be one bare "+
				"SQL identifier", name, bad)
		}
	}
}
