package journal

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A migration exists to upgrade a database created by an older version, so
// it must be tested against a table that does NOT already have the column.
// Applied to a fresh schema the expected result is "duplicate column name",
// which the caller swallows — so a statement that is pure syntax error
// looks identical to one that worked.
func TestMigrationsExecuteOnATableMissingTheColumn(t *testing.T) {
	migrated := map[string]bool{}
	for _, stmt := range migrations {
		if _, column, ok := parseAddColumn(stmt); ok {
			migrated[column] = true
		}
	}

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(stripColumns(schema, migrated)); err != nil {
		t.Fatalf("stripped schema does not execute: %v", err)
	}
	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil {
			t.Errorf("migration does not execute against a table missing the "+
				"column, which is the only case it exists for: %v\n%s", err, stmt)
		}
	}
}

// Every migrated column must also be declared in the canonical schema, so
// a fresh install gets it by CREATE rather than depending on the ALTER, and
// so the schema is a complete description of the table for the next reader.
func TestEveryMigratedColumnIsAlsoDeclaredInSchema(t *testing.T) {
	for _, stmt := range migrations {
		table, column, ok := parseAddColumn(stmt)
		if !ok {
			continue
		}
		block, found := tableBlock(schema, table)
		if !found {
			t.Errorf("migration targets unknown table %q:\n%s", table, stmt)
			continue
		}
		if !strings.Contains(block, "\t"+column+" ") {
			t.Errorf("entries.%s is added by a migration but not declared in "+
				"schema, so a fresh install depends on the ALTER succeeding", column)
		}
	}
}

// NAV-21 on the DDL. A column no agent can universally supply must be
// NULLable: a default writes "measured, and it was nothing" for something
// never measurable, and nothing downstream can tell the two apart.
func TestNewMeasurementColumnsAreNullable(t *testing.T) {
	mustBeNullable := map[string]bool{
		"model": true, "branch": true, "mcp_server": true, "user_modified": true,
		"input_tokens": true, "output_tokens": true,
		"cache_read_tokens": true, "cache_write_tokens": true,
		"reasoning_tokens": true, "duration_ms": true, "time_to_first_token_ms": true,
		"effort": true, "permission_mode": true,
	}

	for _, stmt := range migrations {
		_, column, ok := parseAddColumn(stmt)
		if !ok || !mustBeNullable[column] {
			continue
		}
		if upper := strings.ToUpper(stmt); strings.Contains(upper, "NOT NULL") ||
			strings.Contains(upper, "DEFAULT") {
			t.Errorf("%s is NOT NULL or carries a DEFAULT, but at least one agent "+
				"cannot supply it — the default would be indistinguishable from a "+
				"real measurement (NAV-21):\n%s", column, stmt)
		}
	}
}

// The assertion the whole design rests on: an unset column reads back as
// NULL, not as zero or "".
//
// The DDL tests above check the declaration; this checks the behaviour,
// which is what downstream actually sees. A cost panel that renders 0 for
// an agent with no token data reports "this agent is free" — the exact
// misreading NAV-21 exists to prevent, and it cannot be recovered from
// once the row is written.
func TestUnsuppliedColumnsReadBackAsNullNotZero(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	// A session written the way an agy session would be: engagement counts
	// present, no tokens, no timing, no model.
	if _, err := db.Exec(`INSERT INTO sessions
		(repo_id, session, agent, first_seen, last_seen, user_messages)
		VALUES ('r', 's', 'agy', 1, 2, 3)`); err != nil {
		t.Fatal(err)
	}

	for _, column := range []string{
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"reasoning_tokens", "duration_ms", "time_to_first_token_ms",
		"effort", "permission_mode", "model",
	} {
		var v sql.NullInt64
		var s sql.NullString
		var valid bool

		row := db.QueryRow(`SELECT ` + column + ` FROM sessions WHERE repo_id='r'`)
		if column == "effort" || column == "permission_mode" || column == "model" {
			if err := row.Scan(&s); err != nil {
				t.Errorf("%s: %v", column, err)
				continue
			}
			valid = s.Valid
		} else {
			if err := row.Scan(&v); err != nil {
				t.Errorf("%s: %v", column, err)
				continue
			}
			valid = v.Valid
		}

		if valid {
			t.Errorf("sessions.%s came back non-NULL for an agent that supplies "+
				"nothing; on a cost panel that reads as 'this agent is free' "+
				"(NAV-21)", column)
		}
	}
}

// The same for entries, where branch is the clearest case: agy records no
// branch at all, verified absent rather than merely unread.
func TestEntryColumnsAnAgentCannotSupplyReadBackAsNull(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`INSERT INTO entries
		(repo_id, ts, agent, agent_version, session, event, spec_version)
		VALUES ('r', 1, 'agy', '1.0', 's', 'tool_use', '0.2')`); err != nil {
		t.Fatal(err)
	}

	for _, column := range []string{"model", "branch", "mcp_server", "user_modified"} {
		var s sql.NullString
		if err := db.QueryRow(`SELECT ` + column + ` FROM entries`).Scan(&s); err != nil {
			t.Errorf("%s: %v", column, err)
			continue
		}
		if s.Valid {
			t.Errorf("entries.%s came back non-NULL for an agent that cannot "+
				"supply it, so 'not measurable' is indistinguishable from "+
				"'measured as empty' (NAV-21)", column)
		}
	}
}

func parseAddColumn(stmt string) (table, column string, ok bool) {
	f := strings.Fields(stmt)
	if len(f) < 6 || !strings.EqualFold(f[0], "ALTER") || !strings.EqualFold(f[3], "ADD") {
		return "", "", false
	}
	return f[2], f[5], true
}

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

func tableBlock(schema, table string) (string, bool) {
	marker := "CREATE TABLE IF NOT EXISTS " + table + " ("
	i := strings.Index(schema, marker)
	if i < 0 {
		return "", false
	}
	rest := schema[i+len(marker):]
	if j := strings.Index(rest, ");"); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}
