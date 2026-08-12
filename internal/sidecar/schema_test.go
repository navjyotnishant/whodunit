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
	if count != 4 {
		t.Errorf("found %d tables, want 4", count)
	}
}

func TestNoIdentityColumnOnCommits(t *testing.T) {
	// Contributor belongs on repos, not on every commit row: a repository
	// has one contributor, so repeating it per row is storage spent on a
	// constant. This asserts the shape rather than trusting the DDL text.
	db := openDB(t)
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	rows, err := db.Query(`PRAGMA table_info(whodunit_commits)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch name {
		case "contributor", "author", "committer", "email":
			t.Errorf("whodunit_commits has an identity column %q; it belongs on whodunit_repos", name)
		}
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

	rows := CommitRowsFrom(commits, "repo", now)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Status != string(spec.StatusUndetermined) {
		t.Errorf("untrailed commit got status %q, want undetermined", rows[0].Status)
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

	rows := CommitRowsFrom(commits, "repo", now)
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

	rows := EventRowsFrom(entries, "repo", now)
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
	}}, "repo", now)[0]

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
	}}, "repo", now)[0]

	if _, err := db.Exec(`INSERT INTO whodunit_events
		(repo_id, observed_at, agent, agent_version, session, event, tool, file,
		 lines_added, lines_removed, hunk_hash, spec_version, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		er.RepoID, er.ObservedAt.UnixNano(), er.Agent, er.AgentVersion, er.Session,
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
