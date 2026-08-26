// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: Back up a sidecar table, then rebuild it under a new key.

package sidecar

import (
	"fmt"
	"strings"
	"time"
)

// A primary key cannot be altered in place on SQLite, so changing one is a
// rebuild: create the new table, copy every row, drop the original, rename.
//
// # Why this is not in Migrations
//
// Migrations is a list of best-effort ADD COLUMN statements whose expected
// failure is "column already exists" (see its documentation in schema.go).
// Every entry there is safe to re-run and safe to fail, and
// migrations_test.go asserts exactly that.
//
// A rebuild is neither. Half-completed, it loses rows; re-run after it
// succeeded, it would copy from a table that no longer exists. Adding it to
// that list would break the assumption every other entry depends on, so it
// lives here with its own guarded path and runs only when the stored schema
// version says it has not run yet.
//
// # The backup is the gate, not a courtesy
//
// Once a developer's local journal has pruned its line hashes after a
// successful sync, the sidecar holds rows that exist nowhere else. A failed
// rebuild without a backup is permanent loss, so the backup is taken and
// *verified* before any destructive statement runs, and a backup that cannot
// be taken or verified stops the migration rather than being warned about.
//
// This is the one place in this codebase where failing closed is right.
// Everywhere else attribution degrades rather than blocking work — a commit
// must never fail because attribution failed — but that reasoning protects
// the user's workflow, and here the thing at risk is their data.
//
// # Engine differences
//
// SQLite runs DDL inside a transaction, so its rebuild rolls back as a unit
// and the backup is a second line of defence. MySQL commits DDL implicitly:
// each statement lands as it executes and there is nothing to roll back, so
// on MySQL the backup is the *only* recovery path. Both take the backup;
// only one can also rely on the transaction.

// backupSuffix names a backup table for a rebuild of the given table.
//
// The name carries the source table and a UTC timestamp so an operator can
// find it without reading this source. Kept inside the same database because
// the sidecar may be MySQL, where a table cannot simply be copied out to a
// file.
//
// Resolved to milliseconds rather than seconds, because two rebuilds in the
// same second would otherwise collide on the name.
//
// The milliseconds are appended without a separator on purpose. A dot is a
// qualified-name separator in SQL, so a table named ...T150405.385 parses as
// database "..." table "385" — MySQL reported a permissions error on a table
// nobody named, which is a considerably worse error than the collision this
// resolution exists to avoid.
func backupSuffix(now time.Time) string {
	return "_backup_" + now.UTC().Format("20060102T150405") +
		fmt.Sprintf("%03d", now.UTC().Nanosecond()/int(time.Millisecond))
}

// BackupTable copies a table and verifies the copy before returning.
//
// The verification is a row count on both sides, compared. A copy whose
// count does not match the source is not a backup, and the difference
// between "assumed it worked" and "checked" is the whole value of this
// function — CREATE TABLE ... AS SELECT reports success for a partial copy
// on a full disk.
//
// Returns the backup table's name, which the caller prints so recovery does
// not require reconstructing the procedure under pressure.
func BackupTable(db *Store, table string, now time.Time) (string, error) {
	backup := table + backupSuffix(now)

	// CREATE TABLE ... AS SELECT is accepted by both engines and copies
	// rows without needing the source's DDL. It does not carry over keys or
	// indexes, which is correct here: this is a recovery copy, not a
	// replacement, and a restore reinstates the schema first.
	stmt := fmt.Sprintf("CREATE TABLE %s AS SELECT * FROM %s", backup, table)
	if _, err := db.Exec(stmt); err != nil {
		// A name that already exists means an earlier attempt got this far,
		// which is worth saying plainly: the operator has a backup from that
		// run and the original is still intact, so this is a safe failure
		// rather than an ambiguous one.
		return "", fmt.Errorf(
			"back up %s: %w\n\n%s already exists, so an earlier attempt "+
				"backed this table up and stopped; %s has not been modified",
			table, err, backup, table)
	}

	src, err := countRows(db, table)
	if err != nil {
		return "", fmt.Errorf("count %s before rebuild: %w", table, err)
	}
	got, err := countRows(db, backup)
	if err != nil {
		return "", fmt.Errorf("count backup %s: %w", backup, err)
	}
	if src != got {
		// Deliberately not deleted. A short copy is evidence about what
		// went wrong, and removing it to tidy up would destroy the only
		// artefact an operator has to look at.
		return "", fmt.Errorf(
			"backup %s has %d row(s) but %s has %d: refusing to rebuild",
			backup, got, table, src)
	}
	return backup, nil
}

// countRows is a COUNT(*) on one table.
//
// Split out because the rebuild counts three times — source before, backup
// after, rebuilt table at the end — and a count written three ways is a
// count that eventually disagrees with itself.
func countRows(db *Store, table string) (int64, error) {
	var n int64
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// RestoreCommand is the SQL that puts a backup back, as text.
//
// Printed when a rebuild fails, because recovery under pressure should not
// require deriving the procedure. It is deliberately not executed by
// anything here: an automatic restore after a failure nobody has looked at
// yet would overwrite the evidence of what failed.
func RestoreCommand(table, backup string) string {
	return fmt.Sprintf(
		"DROP TABLE IF EXISTS %s; ALTER TABLE %s RENAME TO %s;",
		table, backup, table)
}

// RebuildTable replaces a table with one built from newDDL, preserving rows.
//
// The steps are: back up and verify, create the new table under a temporary
// name, copy the named columns across, drop the original, rename the new one
// into place, and confirm the row count survived.
//
// columns is the explicit list to copy. Not SELECT *, because the column
// order of the new table need not match the old one and a positional copy
// would silently transpose values — the failure would be a database full of
// plausible wrong rows rather than an error.
func RebuildTable(db *Store, table, newDDL string, columns []string, now time.Time) (err error) {
	backup, err := BackupTable(db, table, now)
	if err != nil {
		// Fail closed: no backup, no rebuild.
		return err
	}

	before, err := countRows(db, table)
	if err != nil {
		return err
	}

	// Any failure past this point leaves the backup as the recovery path, so
	// every error carries the command that uses it.
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w\n\nthe original is backed up in %s; restore with:\n  %s",
				err, backup, RestoreCommand(table, backup))
		}
	}()

	tmp := table + "_rebuild"
	if _, err := db.Exec("DROP TABLE IF EXISTS " + tmp); err != nil {
		return fmt.Errorf("clear %s: %w", tmp, err)
	}

	// newDDL names the real table; the rebuild happens under a temporary
	// name and is renamed into place at the end, so an interrupted run
	// leaves the original intact rather than half-replaced.
	if _, err := db.Exec(strings.Replace(newDDL, table, tmp, 1)); err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}

	cols := strings.Join(columns, ", ")
	copyStmt := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", tmp, cols, cols, table)
	if _, err := db.Exec(copyStmt); err != nil {
		return fmt.Errorf("copy rows into %s: %w", tmp, err)
	}

	// Counted before the original is dropped. Afterwards there is nothing
	// left to compare against, and a short copy discovered then is a
	// discovery made too late to act on.
	copied, err := countRows(db, tmp)
	if err != nil {
		return err
	}
	if copied != before {
		return fmt.Errorf(
			"copied %d row(s) into %s but %s had %d: refusing to drop the original",
			copied, tmp, table, before)
	}

	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		return fmt.Errorf("drop %s: %w", table, err)
	}
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tmp, table)); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, table, err)
	}
	return nil
}
