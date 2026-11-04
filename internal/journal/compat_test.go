package journal

// Compatibility suite (ADR 0008 in whodunit-pro): a journal opened by this
// release with no new config must have exactly today's tables, columns and
// indexes, so a downgrade or a mixed fleet keeps reading it.
//
//	WHODUNIT_UPDATE_COMPAT=1 go test ./internal/journal -run Compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatGoldenJournalSchema(t *testing.T) {
	db, err := open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var tables []string
	for rows.Next() {
		var typ, name, tbl, ddl string
		if err := rows.Scan(&typ, &name, &tbl, &ddl); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "== %s %s (on %s)\n%s\n", typ, name, tbl, ddl)
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()

	for _, tbl := range tables {
		fmt.Fprintf(&b, "== columns %s\n", tbl)
		cols, err := db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY cid`, tbl)
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var name, typ, dflt string
			var notnull, pk int
			if err := cols.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %s notnull=%d default=%q pk=%d\n", name, typ, notnull, dflt, pk)
		}
		cols.Close()
	}
	compatGolden(t, "journal_schema.golden", b.String())

	for _, tbl := range tables {
		if tbl == "agent_lines_keyed" {
			t.Errorf("default journal created opt-in table %q; it must only exist when keyed hashing is enabled", tbl)
		}
	}
}

func compatGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "compat", name)
	if os.Getenv("WHODUNIT_UPDATE_COMPAT") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (generate with WHODUNIT_UPDATE_COMPAT=1)", path, err)
	}
	if string(want) != got {
		t.Fatalf("compatibility break: output differs from %s.\n"+
			"Existing users' stored data depends on this output; see ADR 0008.\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
