// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: Run a schema rebuild once, gated on the stored version.

package sidecar

import (
	"database/sql"
	"fmt"
	"time"
)

// The guard that decides when a rebuild runs.
//
// Migrations handles anything expressible as a best-effort ADD COLUMN. A
// primary-key change is not: it needs a table rebuild, which is destructive
// and must happen exactly once. The difference is not the SQL, it is that
// one is safe to repeat and the other loses rows if it is.
//
// So a rebuild is gated on what the database has actually had applied,
// recorded in whodunit_schema. That is a different fact from the
// schema_version stamped on synced rows: that one says which definition
// produced a number, this one says what shape the tables are in.
//
// # Why the version is written only after the rebuild succeeds
//
// A rebuild that fails leaves the stored version behind, so the next run
// tries again. Recording the version first would turn one failure into a
// permanent skip — the database would claim to be migrated while holding
// the old key, and the collision it was supposed to fix would be back with
// nothing left to notice it.

// firstVersion is what a database with no recorded version is assumed to
// be at.
//
// Not zero, and not the current version. A database without the
// whodunit_schema table predates it, which means it was created under
// version 1 and needs everything since. Treating it as current would skip
// exactly the migration an existing install is here to receive.
const firstVersion = 1

// schemaVersionDDL creates the version table.
//
// The same statement Schema carries. Duplicated as a constant rather than
// parsed back out of Schema, because Migrate needs it before Schema has
// necessarily run and a second definition that drifts is worse than a
// literal that does not.
const schemaVersionDDL = `CREATE TABLE IF NOT EXISTS whodunit_schema (
	id         INTEGER NOT NULL,
	version    BIGINT  NOT NULL,
	applied_at BIGINT  NOT NULL,
	PRIMARY KEY (id)
)`

// rebuild is one destructive migration step.
type rebuild struct {
	// version this step brings the database to.
	version int64
	// table being rebuilt, and the DDL to rebuild it as.
	table   string
	ddl     string
	columns []string
	// why is shown to the operator before anything destructive happens.
	why string
}

// rebuilds are applied in order, each once, to bring a database to
// SchemaVersion.
//
// Kept beside the schema rather than inside Migrations because the two
// have opposite safety properties, and mixing them would mean every entry
// in the combined list had to assume the weaker one.
var rebuilds = []rebuild{{
	version: 2,
	table:   "whodunit_repos",
	ddl: `CREATE TABLE whodunit_repos (
	repo_id      VARCHAR(64)  NOT NULL,
	contributor  VARCHAR(320) NOT NULL DEFAULT '',
	spec_version VARCHAR(16)  NOT NULL DEFAULT '',
	synced_at    BIGINT       NOT NULL,
	PRIMARY KEY (repo_id, contributor)
)`,
	columns: []string{"repo_id", "contributor", "spec_version", "synced_at"},
	why: "whodunit_repos was keyed on repo_id alone, so a second person " +
		"syncing the same repository overwrote the first",
}}

// appliedVersion reads what this database has been migrated to.
//
// A missing table or a missing row both mean "never migrated", which is
// firstVersion rather than an error: an install that predates the version
// table is the ordinary case this whole mechanism exists to handle.
func appliedVersion(db *Store) int64 {
	var v int64
	err := db.QueryRow("SELECT version FROM whodunit_schema WHERE id = 1").Scan(&v)
	if err != nil {
		return firstVersion
	}
	return v
}

// recordVersion stores the version, after the work that earns it.
func recordVersion(db *Store, v int64, now time.Time) error {
	stmt := `INSERT INTO whodunit_schema (id, version, applied_at) VALUES (1, ?, ?)
	         ON CONFLICT(id) DO UPDATE SET version=excluded.version, applied_at=excluded.applied_at`
	if db.mysql {
		stmt = `INSERT INTO whodunit_schema (id, version, applied_at) VALUES (1, ?, ?)
		        ON DUPLICATE KEY UPDATE version=VALUES(version), applied_at=VALUES(applied_at)`
	}
	if _, err := db.Exec(stmt, v, now.UnixNano()); err != nil {
		return fmt.Errorf("record schema version %d: %w", v, err)
	}
	return nil
}

// Migrate applies any rebuild this database has not had, in order.
//
// Returns the versions applied, so a caller can tell an operator what
// happened rather than migrating in silence — a destructive step that
// leaves no trace is one nobody can audit afterwards.
//
// Safe to call on every open: a database already at SchemaVersion does no
// work and touches nothing.
func Migrate(db *Store, now time.Time) (applied []int64, err error) {
	// Created here rather than relying on Schema having run first. The
	// version table is what every rebuild is gated on, so a Migrate that
	// only works when called in the right order is a Migrate that fails
	// quietly when someone calls it in the wrong one.
	if _, err := db.Exec(schemaVersionDDL); err != nil {
		return nil, fmt.Errorf("create the schema version table: %w", err)
	}

	at := appliedVersion(db)

	for _, r := range rebuilds {
		if at >= r.version {
			continue
		}
		// A fresh database already has the current shape from Schema, so
		// rebuilding it would be work with no effect — and a needless
		// destructive step is still a destructive step.
		needed, err := tableNeedsRebuild(db, r)
		if err != nil {
			return applied, err
		}
		if needed {
			if err := RebuildTable(db, r.table, r.ddl, r.columns, now); err != nil {
				// Deliberately not recording the version: the next run has
				// to try again rather than skip a migration that did not
				// happen.
				return applied, fmt.Errorf("migrate to version %d (%s): %w",
					r.version, r.why, err)
			}
		}
		if err := recordVersion(db, r.version, now); err != nil {
			return applied, err
		}
		applied = append(applied, r.version)
		at = r.version
	}
	return applied, nil
}

// tableNeedsRebuild reports whether the table is still in its old shape.
//
// Asked of the database rather than inferred from the version, because the
// two can disagree in the direction that matters: a fresh install gets the
// new shape from Schema while recording no version at all, and rebuilding
// it would copy a table onto itself for nothing.
//
// The test is whether the new key's columns already form the primary key.
// Both engines can be asked, in different dialects.
func tableNeedsRebuild(db *Store, r rebuild) (bool, error) {
	var keyCols []string
	var err error
	if db.mysql {
		keyCols, err = mysqlPrimaryKey(db, r.table)
	} else {
		keyCols, err = sqlitePrimaryKey(db, r.table)
	}
	if err != nil {
		return false, err
	}
	if len(keyCols) == 0 {
		// No such table: nothing to rebuild. Schema will create it in the
		// current shape.
		return false, nil
	}
	// The rebuild is needed exactly when the current key is narrower than
	// the one the new DDL declares. Counting is enough here because every
	// rebuild so far widens a key rather than reordering one; a future
	// rebuild that reorders would need the columns compared, and this is
	// the place that would have to change.
	return len(keyCols) < 2, nil
}

func mysqlPrimaryKey(db *Store, table string) ([]string, error) {
	rows, err := db.Query(`
		SELECT column_name FROM information_schema.key_column_usage
		WHERE table_schema = DATABASE() AND table_name = ?
		  AND constraint_name = 'PRIMARY'
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func sqlitePrimaryKey(db *Store, table string) ([]string, error) {
	rows, err := db.Query(
		`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
