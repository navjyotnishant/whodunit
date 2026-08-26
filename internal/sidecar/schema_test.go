package sidecar

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/navjyotnishant/whodunit/internal/spec"
	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sidecar.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSchemaExecutes(t *testing.T) {
	// An unexecuted DDL is a guess. This is the test that makes the schema
	// a fact.
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema does not execute: %v", err)
	}
	for _, stmt := range Indexes {
		if _, err := db.Exec(stmt); err != nil {
			t.Errorf("index does not execute: %v\n%s", err, stmt)
		}
	}
}

func TestSchemaAvoidsMySQLIncompatibilities(t *testing.T) {
	// These are portability bugs found by applying the schema to a real
	// DevLake MySQL, not hypotheses. SQLite accepts all three, so only an
	// assertion on the DDL text catches a regression here.

	// MySQL rejects DEFAULT on TEXT/BLOB columns.
	if strings.Contains(Schema, "TEXT NOT NULL DEFAULT") {
		t.Error("a TEXT column has a DEFAULT; MySQL rejects that")
	}

	// MySQL has no CREATE INDEX IF NOT EXISTS, and inline INDEX clauses
	// are MySQL-only — so neither may appear in the shared DDL.
	if strings.Contains(Schema, "CREATE INDEX") {
		t.Error("Schema declares an index; indexes belong in Indexes so each engine can apply them its own way")
	}
	if strings.Contains(Schema, "\tINDEX ") {
		t.Error("Schema uses an inline INDEX clause, which SQLite rejects")
	}

	// A file path in a composite primary key blows MySQL's 3072-byte key
	// limit, which is why events are keyed on a derived hash.
	if strings.Contains(Schema, "PRIMARY KEY (repo_id, session, observed_at") {
		t.Error("events are keyed on a composite including the file path; that exceeds MySQL's key length limit")
	}
}

func TestSchemaIsIdempotent(t *testing.T) {
	// Sync runs repeatedly against a store that may already be set up.
	db := openDB(t)
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(Schema); err != nil {
			t.Fatalf("schema apply #%d: %v", i, err)
		}
	}
}

func TestEveryTableIsNamespaced(t *testing.T) {
	// DevLake shares the database. An unprefixed table would eventually
	// collide with theirs or a plugin's (NAV-37).
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.HasPrefix(name, "sqlite_") {
			continue // SQLite's own bookkeeping
		}
		if !strings.HasPrefix(name, TablePrefix) {
			t.Errorf("table %q is not namespaced with %q", name, TablePrefix)
		}
		count++
	}
	// repos, commits, events, sessions, event_lines, baselines, schema,
	// identities.
	//
	// The literal is deliberate: a table appearing without anyone updating
	// this number means a table was added without deciding whether it
	// belongs in a shared DevLake database, which is the question the
	// prefix rule exists to force.
	//
	// whodunit_schema was the seventh, and the answer is yes: it records
	// which migrations this database has had applied, which is a fact
	// about whodunit's tables rather than about the database it shares
	// (WHO-220). It carries the prefix for the same reason every other
	// table does.
	//
	// whodunit_identities was the eighth, and yes again: it maps between
	// addresses that already appear in every commit object, so a
	// dashboard filtered to one person includes every address they commit
	// from (WHO-208).
	if count != 8 {
		t.Errorf("found %d tables, want 8", count)
	}
}

func TestNoIdentityColumnOnCommits(t *testing.T) {
	// Contributor is now carried on commits, and that reverses what this
	// test used to assert.
	//
	// It guarded the premise that a repository has one contributor, so
	// repeating it per row is storage spent on a constant. True locally,
	// false in the shared database this sidecar populates: repo_id is the
	// root commit SHA, identical for everyone who clones the repository,
	// so identity resolved through a join was identity resolved through a
	// row two people share (WHO-192,
	// docs/decisions/0001-contributor-key.md).
	//
	// What has NOT changed is the rest of the rule: contributor is the one
	// identity fact this table carries, and author, committer and email
	// stay off it. The git objects already hold those, and duplicating
	// them here would be new surveillance surface rather than a join
	// removed. That is what this test guards now.
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	rows, err := db.Query(`PRAGMA table_info(whodunit_commits)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()

	var sawContributor bool
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch name {
		case "author", "committer", "email":
			t.Errorf("whodunit_commits has an identity column %q; contributor "+
				"is the only identity this table carries, and git already "+
				"holds the rest", name)
		}
		if name == "contributor" {
			sawContributor = true
		}
	}
	if !sawContributor {
		t.Error("whodunit_commits has no contributor column; the dashboard " +
			"grain would be back to resolving identity through a join that " +
			"two people share")
	}
}

func TestRatioColumnIsNullable(t *testing.T) {
	// A commit with no line-level evidence has no share to report, and 0.0
	// would assert the agent contributed nothing (NAV-8).
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO whodunit_commits
		(commit_sha, repo_id, committed_at, status, method, schema_version, synced_at)
		VALUES ('abc', 'repo', 0, 'undetermined', 'undetermined', 1, 0)`); err != nil {
		t.Fatalf("insert with null ratio: %v", err)
	}

	var ratio sql.NullFloat64
	if err := db.QueryRow(`SELECT ratio FROM whodunit_commits WHERE commit_sha='abc'`).Scan(&ratio); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if ratio.Valid {
		t.Error("ratio came back non-null after being omitted")
	}
}

func TestCommitPrimaryKeyIsRepoScoped(t *testing.T) {
	// Two repositories can legitimately contain the same commit — a fork,
	// a cherry-pick, a vendored subtree. Keying on sha alone would make one
	// silently overwrite the other.
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	for _, repo := range []string{"repo-a", "repo-b"} {
		if _, err := db.Exec(`INSERT INTO whodunit_commits
			(commit_sha, repo_id, committed_at, status, method, schema_version, synced_at)
			VALUES ('shared-sha', ?, 0, 'assisted', 'observed', 1, 0)`, repo); err != nil {
			t.Fatalf("insert for %s: %v", repo, err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM whodunit_commits WHERE commit_sha='shared-sha'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("the same sha in two repos produced %d rows, want 2", n)
	}
}

func TestCommitRowsFromKeepsUntrailedCommits(t *testing.T) {
	// Coverage's denominator is every commit, not every commit we happened
	// to understand. Dropping untrailed commits would make it uncomputable
	// and silently overstate coverage (NAV-21).
	now := time.Now()
	commits := []report.Commit{
		{SHA: "aaa", Timestamp: now, Purpose: purpose.Feature, LinesAdded: 10},
		{SHA: "bbb", Timestamp: now, Purpose: purpose.Fix, Trailer: &spec.Trailer{
			Status: spec.StatusAssisted, Method: spec.MethodObserved, Agent: "claude-code",
		}},
	}

	rows := CommitRowsFrom(commits, "repo", "dev@example.com", now)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	// uninstrumented rather than undetermined (WHO-211): no trailer means
	// nothing was watching, which is different from having looked and
	// found nothing. Either way it must not count as attributed, which is
	// what this test is really protecting.
	if rows[0].Status != string(spec.StatusUninstrumented) {
		t.Errorf("untrailed commit got status %q, want uninstrumented", rows[0].Status)
	}
	if spec.Status(rows[0].Status).Attributed() {
		t.Error("an untrailed commit must never count as attributed")
	}
	if rows[1].Agent != "claude-code" {
		t.Errorf("trailered commit lost its agent: %+v", rows[1])
	}
}

func TestCommitRowsCarryRatioOnlyWhenPresent(t *testing.T) {
	now := time.Now()
	r := 0.42
	commits := []report.Commit{
		{SHA: "with", Trailer: &spec.Trailer{Status: spec.StatusAssisted, Method: spec.MethodIntersected, Ratio: &r}},
		{SHA: "without", Trailer: &spec.Trailer{Status: spec.StatusAssisted, Method: spec.MethodObserved}},
	}

	rows := CommitRowsFrom(commits, "repo", "dev@example.com", now)
	if rows[0].Ratio == nil || *rows[0].Ratio != 0.42 {
		t.Errorf("ratio lost in mapping: %+v", rows[0].Ratio)
	}
	if rows[1].Ratio != nil {
		t.Errorf("ratio invented where there was none: %v", *rows[1].Ratio)
	}
}

func TestEventRowsPreserveTheJournalGrain(t *testing.T) {
	now := time.Now()
	entries := []journal.Entry{
		{Timestamp: now, Agent: "claude-code", Session: "s1", Event: "tool_use",
			Tool: "Edit", File: "/repo/main.go", LinesAdded: 3, HunkHash: "sha256:x"},
	}

	rows := EventRowsFrom(entries, "repo", "dev@example.com", now)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].File != "/repo/main.go" || rows[0].Tool != "Edit" {
		t.Errorf("event row lost detail: %+v", rows[0])
	}
}

func TestLineRowsFromDeduplicatedSet(t *testing.T) {
	now := time.Now()
	hashes := map[uint64]struct{}{1: {}, 2: {}, 3: {}}
	rows := LineRowsFrom(hashes, "repo", now)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
}

func TestSchemaAcceptsEveryRowType(t *testing.T) {
	// End to end: the mapped rows must actually insert against the DDL.
	// Column-name typos are otherwise invisible until a live sync.
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	now := time.Now()

	if _, err := db.Exec(`INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
		VALUES (?, ?, ?, ?)`, "repo", "dev@example.com", "0.2", now.UnixNano()); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	r := 0.5
	cr := CommitRowsFrom([]report.Commit{{
		SHA: "abc", Timestamp: now, Purpose: purpose.Feature, LinesAdded: 10, LinesRemoved: 2,
		Files:   []string{"a.go"},
		Trailer: &spec.Trailer{Status: spec.StatusAssisted, Method: spec.MethodIntersected, Agent: "claude-code", Ratio: &r},
	}}, "repo", "dev@example.com", now)[0]

	if _, err := db.Exec(`INSERT INTO whodunit_commits
		(commit_sha, repo_id, committed_at, status, method, agent, agent_version,
		 purpose, ratio, lines_added, lines_removed, files_changed, spec_version,
		 schema_version, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cr.CommitSHA, cr.RepoID, cr.CommittedAt.UnixNano(), cr.Status, cr.Method,
		cr.Agent, cr.AgentVersion, cr.Purpose, cr.Ratio, cr.LinesAdded, cr.LinesRemoved,
		cr.FilesChanged, cr.SpecVersion, cr.SchemaVersion, cr.SyncedAt.UnixNano()); err != nil {
		t.Fatalf("insert commit: %v", err)
	}

	er := EventRowsFrom([]journal.Entry{{
		Timestamp: now, Agent: "claude-code", Session: "s", Event: "tool_use",
		Tool: "Write", File: "/repo/a.go", LinesAdded: 10,
	}}, "repo", "dev@example.com", now)[0]

	if _, err := db.Exec(`INSERT INTO whodunit_events
		(event_id, repo_id, observed_at, agent, agent_version, session, event, tool, file,
		 lines_added, lines_removed, hunk_hash, spec_version, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		er.EventID, er.RepoID, er.ObservedAt.UnixNano(), er.Agent, er.AgentVersion, er.Session,
		er.Event, er.Tool, er.File, er.LinesAdded, er.LinesRemoved, er.HunkHash,
		er.SpecVersion, er.SyncedAt.UnixNano()); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	lr := LineRowsFrom(map[uint64]struct{}{99: {}}, "repo", now)[0]
	if _, err := db.Exec(`INSERT INTO whodunit_event_lines (repo_id, line_hash, first_at, synced_at)
		VALUES (?, ?, ?, ?)`,
		lr.RepoID, int64(lr.Hash), lr.FirstAt.UnixNano(), lr.SyncedAt.UnixNano()); err != nil {
		t.Fatalf("insert line: %v", err)
	}
}

func TestEventIDIsStableAndDistinguishing(t *testing.T) {
	// Sync must be re-runnable: the same observation has to produce the
	// same id so it collides on the primary key instead of duplicating.
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	e := journal.Entry{
		Timestamp: now, Session: "s1", Tool: "Edit",
		File: "/repo/main.go", HunkHash: "sha256:abc",
	}

	if eventID("repo", e) != eventID("repo", e) {
		t.Error("the same observation produced two different ids")
	}

	// Every component of the identity must actually change the id.
	variants := map[string]journal.Entry{
		"session":   {Timestamp: now, Session: "s2", Tool: "Edit", File: "/repo/main.go", HunkHash: "sha256:abc"},
		"timestamp": {Timestamp: now.Add(time.Second), Session: "s1", Tool: "Edit", File: "/repo/main.go", HunkHash: "sha256:abc"},
		"tool":      {Timestamp: now, Session: "s1", Tool: "Write", File: "/repo/main.go", HunkHash: "sha256:abc"},
		"file":      {Timestamp: now, Session: "s1", Tool: "Edit", File: "/repo/other.go", HunkHash: "sha256:abc"},
		"hunk":      {Timestamp: now, Session: "s1", Tool: "Edit", File: "/repo/main.go", HunkHash: "sha256:xyz"},
	}
	base := eventID("repo", e)
	for field, variant := range variants {
		if eventID("repo", variant) == base {
			t.Errorf("changing %s did not change the event id", field)
		}
	}

	// And the repository must scope it, or two repos' events collide.
	if eventID("repo-b", e) == base {
		t.Error("the same observation in two repositories produced the same id")
	}
}

func TestEventIDSeparatesFields(t *testing.T) {
	// Without a separator, ("ab","c") and ("a","bc") would hash alike.
	now := time.Now()
	a := journal.Entry{Timestamp: now, Session: "ab", Tool: "c"}
	b := journal.Entry{Timestamp: now, Session: "a", Tool: "bc"}
	if eventID("repo", a) == eventID("repo", b) {
		t.Error("adjacent fields collided; the id is not separator-delimited")
	}
}

// A semicolon inside a SQL comment truncates the statement it sits in.
//
// splitStatements splits on ";" before it strips comments, so a comment
// containing one is cut in half and the tail — including the CREATE TABLE
// that followed it — is sent to the engine as its own statement. The
// failure reads as a syntax error on a fragment of English prose, which
// gives no hint where it came from. Cost a confused debugging round on
// WHO-208, where a comment ended "...every commit object; this is a lookup
// table".
func TestNoSemicolonInsideASchemaComment(t *testing.T) {
	for i, line := range strings.Split(Schema, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--") {
			continue
		}
		if strings.Contains(trimmed, ";") {
			t.Errorf("line %d has a semicolon inside a comment, which "+
				"truncates the statement it belongs to:\n  %s", i+1, trimmed)
		}
	}
}

// Every alias resolves to a canonical address, never to itself.
//
// A row mapping an address to itself carries no information, and the join
// treats a missing row as "its own identity" anyway — so writing it would
// only make absence harder to read (NAV-21).
func TestIdentityRowsSkipSelfReferences(t *testing.T) {
	resolve := func(s string) string {
		if s == "b@x.com" {
			return "a@x.com"
		}
		return s
	}
	rows := IdentityRowsFrom(
		map[string]string{"b@x.com": "a@x.com", "a@x.com": "a@x.com"},
		resolve, time.Now())

	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want 1 — a self-reference was written", len(rows))
	}
	if rows[0].Alias != "b@x.com" || rows[0].Canonical != "a@x.com" {
		t.Errorf("got %v", rows[0])
	}
}

// A chain must be flattened, so SQL never has to follow one.
//
// Writing c -> b verbatim would make the dashboard responsible for
// following the chain to a, which MySQL and SQLite express differently and
// neither expresses simply. Flattening here means the dashboard joins once
// and cannot disagree with config.ResolveIdentity about the answer.
func TestIdentityChainsAreFlattened(t *testing.T) {
	// c -> b -> a, as config.ResolveIdentity would resolve it.
	resolve := func(s string) string {
		for _, step := range []struct{ from, to string }{{"c@x.com", "b@x.com"}, {"b@x.com", "a@x.com"}} {
			if s == step.from {
				s = step.to
			}
		}
		if s == "b@x.com" {
			s = "a@x.com"
		}
		return s
	}
	rows := IdentityRowsFrom(map[string]string{"c@x.com": "b@x.com"}, resolve, time.Now())

	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want 1", len(rows))
	}
	if rows[0].Canonical != "a@x.com" {
		t.Errorf("chain resolved to %q, want a@x.com — SQL would have to "+
			"follow the chain itself", rows[0].Canonical)
	}
}

// No configured aliases means no rows: the feature is inert until used.
func TestNoAliasesMeansNoRows(t *testing.T) {
	if rows := IdentityRowsFrom(nil, func(s string) string { return s }, time.Now()); rows != nil {
		t.Errorf("got %d row(s) from an empty map", len(rows))
	}
}
