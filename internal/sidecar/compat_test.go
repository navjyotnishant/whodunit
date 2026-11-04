package sidecar

// Compatibility suite (ADR 0008 in whodunit-pro): with no new config set,
// `dun sync` must create exactly today's DevLake tables and write exactly
// today's SQL. Opt-in features may add tables and statements, but only
// behind their own flags, never in the default path snapshotted here.
//
//	WHODUNIT_UPDATE_COMPAT=1 go test ./internal/sidecar -run Compat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/compattest"
)

func TestCompatGoldenSidecarSchemaAndSQL(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, "SchemaVersion=%d TablePrefix=%s\n\n== Schema\n%s\n", SchemaVersion, TablePrefix, Schema)
	b.WriteString("== Migrations\n")
	for _, m := range Migrations {
		b.WriteString(m + "\n")
	}
	b.WriteString("== Indexes\n")
	for _, ix := range Indexes {
		b.WriteString(ix + "\n")
	}
	builders := []struct {
		name string
		fn   func(bool) string
	}{
		{"upsertRepo", upsertRepo},
		{"upsertIdentity", upsertIdentity},
		{"upsertCommit", upsertCommit},
		{"upsertEvent", upsertEvent},
		{"upsertSession", upsertSession},
		{"upsertBaseline", upsertBaseline},
		{"upsertLine", upsertLine},
	}
	for _, s := range builders {
		for _, mysql := range []bool{false, true} {
			fmt.Fprintf(&b, "== %s(mysql=%v)\n%s\n", s.name, mysql, s.fn(mysql))
		}
	}
	compattest.Golden(t, "sidecar.golden", b.String())
}

// Tables ADR 0008 adds only when opted in. None may appear in the default schema.
var compatOptInTables = []string{"commit_lines", "agent_lines_keyed"}

func TestCompatDefaultSchemaHasNoOptInTables(t *testing.T) {
	all := Schema + strings.Join(Migrations, "\n") + strings.Join(Indexes, "\n")
	for _, name := range compatOptInTables {
		if strings.Contains(all, TablePrefix+name) || strings.Contains(all, name) {
			t.Errorf("default sidecar schema mentions opt-in table %q; it must only be created when enabled", name)
		}
	}
}

// The opt-in commit lines table is a stored format once enabled, so it is
// pinned too, separately from the default schema above.
func TestCompatGoldenCommitLinesSchemaAndSQL(t *testing.T) {
	got := "== CommitLinesSchema\n" + CommitLinesSchema + "\n== index\n" + commitLinesIndex + "\n" +
		"== upsertCommitLine(mysql=false)\n" + upsertCommitLine(false) + "\n" +
		"== upsertCommitLine(mysql=true)\n" + upsertCommitLine(true) + "\n"
	compattest.Golden(t, "sidecar_commit_lines.golden", got)
}
