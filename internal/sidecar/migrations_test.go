package sidecar

import (
	"database/sql"
	"strings"
	"testing"
)

// Migrations had no test at all, which is how a statement valid on SQLite
// and rejected by MySQL would reach a real DevLake unnoticed. Sync applies
// them best-effort and ignores the error — the expected failure is "column
// already exists" — so a genuinely broken statement is silent, and the
// column it should have added is simply never there. The sync then writes
// NULL into a column that does not exist, or drops the value.
//
// These assert the two halves separately: the statements execute, and they
// stay inside the SQLite ∩ MySQL intersection.

// A migration must execute against a table that does NOT already have the
// column — the case it exists for. Applied to a fresh database, where
// Schema has already declared the column, the expected result is
// "duplicate column name", which sync swallows.
//
// So the table is created WITHOUT the migrated columns, by stripping them
// from Schema, and every migration must then succeed. Running them against
// a fresh Schema instead would pass for a statement that is pure syntax
// error, because both fail and both are ignored.
func TestMigrationsExecuteOnATableMissingTheColumn(t *testing.T) {
	db := openDB(t)

	migrated := map[string]bool{}
	for _, stmt := range Migrations {
		if _, column, ok := parseAddColumn(stmt); ok {
			migrated[column] = true
		}
	}
	if _, err := db.Exec(stripColumns(Schema, migrated)); err != nil {
		t.Fatalf("stripped schema does not execute: %v", err)
	}

	for _, stmt := range Migrations {
		if _, err := db.Exec(stmt); err != nil {
			t.Errorf("migration does not execute against a table missing the "+
				"column, which is the only case it exists for: %v\n%s", err, stmt)
		}
	}
}

// stripColumns removes the named column declarations from a CREATE TABLE
// body, producing the older schema a migration is meant to upgrade.
func stripColumns(schema string, drop map[string]bool) string {
	var out []string
	for _, line := range strings.Split(schema, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && drop[fields[0]] {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Every migrated column must ALSO be declared in Schema.
//
// The two are not redundant, they cover different databases: Schema builds
// a fresh one, the migration upgrades an existing one. Declaring a column
// only in a migration means a fresh install gets it by ALTER rather than
// by CREATE — which works, but leaves the canonical schema an incomplete
// description of the table, and that is where the next reader looks.
//
// The reverse — a migration for a column Schema never declares — is the
// one that silently loses data on a fresh install if the ALTER ever fails.
func TestEveryMigratedColumnIsAlsoDeclaredInSchema(t *testing.T) {
	for _, stmt := range Migrations {
		table, column, ok := parseAddColumn(stmt)
		if !ok {
			continue
		}
		block, found := tableBlock(Schema, table)
		if !found {
			t.Errorf("migration targets unknown table %q:\n%s", table, stmt)
			continue
		}
		if !strings.Contains(block, "\t"+column+" ") {
			t.Errorf("%s.%s is added by a migration but not declared in Schema, "+
				"so the canonical schema does not describe the table and a fresh "+
				"install depends on the ALTER succeeding", table, column)
		}
	}
}

func TestMigrationsStayInsideTheSQLiteMySQLIntersection(t *testing.T) {
	for _, stmt := range Migrations {
		upper := strings.ToUpper(stmt)

		// MySQL accepts several ADD COLUMN clauses in one ALTER; SQLite
		// takes exactly one.
		if strings.Count(upper, "ADD COLUMN") > 1 {
			t.Errorf("more than one ADD COLUMN in a single statement; SQLite rejects that:\n%s", stmt)
		}

		// AFTER <column> and FIRST are MySQL-only positioning clauses.
		if strings.Contains(upper, " AFTER ") || strings.HasSuffix(upper, " FIRST") {
			t.Errorf("column positioning clause is MySQL-only:\n%s", stmt)
		}

		// Neither engine supports IF NOT EXISTS on ADD COLUMN in the
		// version range this targets — which is why these are best-effort
		// rather than conditional.
		if strings.Contains(upper, "IF NOT EXISTS") {
			t.Errorf("ADD COLUMN IF NOT EXISTS is not portable here:\n%s", stmt)
		}

		// MySQL rejects a DEFAULT on TEXT/BLOB. The same rule Schema is
		// already held to.
		if strings.Contains(upper, "TEXT NOT NULL DEFAULT") {
			t.Errorf("a TEXT column carries a DEFAULT; MySQL rejects that:\n%s", stmt)
		}

		// MySQL needs a length on a string column used in any index, and
		// bare TEXT cannot carry one. VARCHAR(n) works on both engines.
		if strings.Contains(upper, "ADD COLUMN") && strings.Contains(upper, " TEXT") {
			t.Errorf("bare TEXT in a sidecar migration; use VARCHAR(n) so MySQL "+
				"can index it:\n%s", stmt)
		}
	}
}

// NAV-21, asserted on the DDL rather than trusted to review.
//
// A column an agent cannot supply must be NULLable. NOT NULL DEFAULT ”
// or DEFAULT 0 writes "measured, and it was nothing" for something never
// measurable, and nothing downstream can tell that apart afterwards — a
// zero on a cost panel reads as "this agent is free".
func TestNewMeasurementColumnsAreNullable(t *testing.T) {
	// Columns where at least one supported agent has no data at all.
	// agy supplies no branch, no tokens and no timing; only Claude Code
	// reports user_modified; only Codex reports reasoning and timing.
	mustBeNullable := []string{
		"model", "branch", "mcp_server", "user_modified",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"reasoning_tokens", "duration_ms", "time_to_first_token_ms",
		"effort", "permission_mode",
	}

	for _, stmt := range Migrations {
		_, column, ok := parseAddColumn(stmt)
		if !ok {
			continue
		}
		if !contains(mustBeNullable, column) {
			continue
		}
		upper := strings.ToUpper(stmt)
		if strings.Contains(upper, "NOT NULL") {
			t.Errorf("%s is NOT NULL, but at least one agent cannot supply it — "+
				"a default would be indistinguishable from a real measurement "+
				"(NAV-21):\n%s", column, stmt)
		}
		if strings.Contains(upper, "DEFAULT") {
			t.Errorf("%s carries a DEFAULT, which makes an unsupplied value look "+
				"measured (NAV-21):\n%s", column, stmt)
		}
	}
}

// A column added to the journal must also reach the sidecar, or the data is
// collected locally and silently dropped at sync — the worst of both, since
// the local journal looks complete.
func TestSidecarCoversEveryNewJournalColumn(t *testing.T) {
	// Kept as a literal list rather than derived from the journal package:
	// deriving it would make the test agree with whatever the journal does,
	// including forgetting a column. The point is that adding one here is a
	// deliberate step.
	want := map[string][]string{
		"whodunit_events": {"model", "branch", "mcp_server", "user_modified"},
		"whodunit_sessions": {
			"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
			"reasoning_tokens", "duration_ms", "time_to_first_token_ms",
			"effort", "permission_mode", "model",
		},
	}

	got := map[string]map[string]bool{}
	for _, stmt := range Migrations {
		table, column, ok := parseAddColumn(stmt)
		if !ok {
			continue
		}
		if got[table] == nil {
			got[table] = map[string]bool{}
		}
		got[table][column] = true
	}

	for table, columns := range want {
		for _, c := range columns {
			if !got[table][c] {
				t.Errorf("%s.%s is collected in the journal but never reaches the "+
					"sidecar, so it is dropped at sync", table, c)
			}
		}
	}
}

// Applying the whole set twice must be safe: sync runs against a store that
// may already be migrated, and the second run's failures are expected and
// ignored. What must NOT happen is the second run leaving the schema
// different from the first.
func TestMigrationsAreSafeToReapply(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}

	apply := func() {
		for _, stmt := range Migrations {
			db.Exec(stmt) // best-effort, exactly as sync does
		}
	}
	apply()
	first := columnsOf(t, db, "whodunit_sessions")
	apply()
	second := columnsOf(t, db, "whodunit_sessions")

	if len(first) != len(second) {
		t.Errorf("re-applying migrations changed the column set: %d then %d", len(first), len(second))
	}
}

func columnsOf(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// parseAddColumn pulls the table and column out of
// `ALTER TABLE <t> ADD COLUMN <c> <type...>`.
func parseAddColumn(stmt string) (table, column string, ok bool) {
	fields := strings.Fields(stmt)
	if len(fields) < 6 {
		return "", "", false
	}
	if !strings.EqualFold(fields[0], "ALTER") || !strings.EqualFold(fields[1], "TABLE") {
		return "", "", false
	}
	if !strings.EqualFold(fields[3], "ADD") || !strings.EqualFold(fields[4], "COLUMN") {
		return "", "", false
	}
	return fields[2], fields[5], true
}

// tableBlock returns the body of a CREATE TABLE statement for the named
// table.
func tableBlock(schema, table string) (string, bool) {
	marker := "CREATE TABLE IF NOT EXISTS " + table + " ("
	i := strings.Index(schema, marker)
	if i < 0 {
		return "", false
	}
	rest := schema[i+len(marker):]
	j := strings.Index(rest, ");")
	if j < 0 {
		return rest, true
	}
	return rest[:j], true
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
