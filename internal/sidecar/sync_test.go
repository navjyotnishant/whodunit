package sidecar

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open("sqlite:///" + filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return db
}

func samplePayload(now time.Time) Payload {
	r := 0.5
	return Payload{
		Repo: RepoRow{RepoID: "repo", Contributor: "dev@example.com", SpecVersion: "0.2", SyncedAt: now},
		Commits: []CommitRow{{
			CommitSHA: "abc", RepoID: "repo", CommittedAt: now,
			Status: "assisted", Method: "intersected", Agent: "claude-code",
			Ratio: &r, LinesAdded: 10, LinesRemoved: 2, FilesChanged: 1,
			SchemaVersion: SchemaVersion, SyncedAt: now,
		}},
		Events: []EventRow{{
			EventID: "e1", RepoID: "repo", ObservedAt: now, Agent: "claude-code",
			Event: "tool_use", Tool: "Edit", File: "/repo/main.go",
			LinesAdded: 10, SyncedAt: now,
		}},
		Lines: []LineRow{{RepoID: "repo", Hash: 42, FirstAt: now, SyncedAt: now}},
	}
}

func TestOpenRejectsUnknownScheme(t *testing.T) {
	if _, err := Open("postgres://user@host/db"); err == nil {
		t.Error("Open accepted an unsupported scheme")
	}
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	// Sync runs against a store that may already be set up, possibly by
	// someone else's earlier sync.
	db := openStore(t)
	for i := 0; i < 3; i++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("EnsureSchema #%d: %v", i, err)
		}
	}
}

func TestWriteThenRead(t *testing.T) {
	db := openStore(t)
	now := time.Now().UTC()

	counts, err := Write(db, samplePayload(now))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if counts.Commits != 1 || counts.Events != 1 || counts.Lines != 1 {
		t.Errorf("counts = %+v, want 1 of each", counts)
	}

	var contributor string
	if err := db.QueryRow(`SELECT contributor FROM whodunit_repos WHERE repo_id='repo'`).Scan(&contributor); err != nil {
		t.Fatalf("read repo: %v", err)
	}
	if contributor != "dev@example.com" {
		t.Errorf("contributor = %q", contributor)
	}

	var ratio float64
	if err := db.QueryRow(`SELECT ratio FROM whodunit_commits WHERE commit_sha='abc'`).Scan(&ratio); err != nil {
		t.Fatalf("read commit: %v", err)
	}
	if ratio != 0.5 {
		t.Errorf("ratio = %v, want 0.5", ratio)
	}
}

func TestWriteIsIdempotent(t *testing.T) {
	// The local journal is the source of truth and a sync is a projection
	// of it, so running one twice must not double anything.
	db := openStore(t)
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		if _, err := Write(db, samplePayload(now)); err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
	}

	for table, want := range map[string]int{
		"whodunit_repos": 1, "whodunit_commits": 1,
		"whodunit_events": 1, "whodunit_event_lines": 1,
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != want {
			t.Errorf("%s has %d rows after 3 syncs, want %d", table, n, want)
		}
	}
}

func TestWriteUpdatesChangedValues(t *testing.T) {
	// A commit amended locally, or a trailer corrected, must overwrite
	// rather than being ignored as a duplicate.
	db := openStore(t)
	now := time.Now().UTC()

	p := samplePayload(now)
	if _, err := Write(db, p); err != nil {
		t.Fatalf("first write: %v", err)
	}

	p.Commits[0].Method = "observed"
	p.Commits[0].Ratio = nil
	if _, err := Write(db, p); err != nil {
		t.Fatalf("second write: %v", err)
	}

	var method string
	var ratio *float64
	if err := db.QueryRow(`SELECT method, ratio FROM whodunit_commits WHERE commit_sha='abc'`).
		Scan(&method, &ratio); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if method != "observed" {
		t.Errorf("method = %q, want the updated value", method)
	}
	if ratio != nil {
		t.Errorf("ratio = %v, want nil after being cleared", *ratio)
	}
}

func TestWriteRollsBackOnFailure(t *testing.T) {
	// A sync that fails partway must leave nothing behind: a partial sync
	// that looks complete is worse than one that plainly failed.
	db := openStore(t)
	now := time.Now().UTC()

	// Drop the events table so the write fails after commits are inserted
	// but before it can finish. Anything already written must roll back.
	if _, err := db.Exec(`DROP TABLE whodunit_events`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	if _, err := Write(db, samplePayload(now)); err == nil {
		t.Fatal("Write succeeded against a missing table")
	}

	for _, table := range []string{"whodunit_commits", "whodunit_repos", "whodunit_event_lines"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s kept %d rows from a failed sync", table, n)
		}
	}
}

func TestSplitStatementsDropsCommentOnlyFragments(t *testing.T) {
	// The MySQL driver refuses multiple statements per Exec, and a
	// trailing comment block would otherwise be sent as a statement.
	stmts := splitStatements(Schema)
	if len(stmts) != 7 {
		t.Fatalf("want 7 statements for 7 tables, got %d", len(stmts))
	}
	for _, s := range stmts {
		if s == "" {
			t.Error("produced an empty statement")
		}
	}
}

func TestLastSyncReadsTheRepoRowNotTheCommits(t *testing.T) {
	// The value is available two ways: a single indexed row in
	// whodunit_repos, or MAX(synced_at) over every commit the repository
	// has. They agree, so a test that only checks the returned time would
	// pass either way — and the aggregate walks every row of a table that
	// holds a whole team's history, on a command that runs constantly.
	//
	// So this pins the source: the commits carry a deliberately later
	// synced_at than the repo row. Reading the repo row returns the earlier
	// one; going back to the aggregate returns the later one and fails here.
	db := openStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	p := samplePayload(now)
	if _, err := Write(db, p); err != nil {
		t.Fatalf("Write: %v", err)
	}

	later := now.Add(48 * time.Hour)
	if _, err := db.Exec(
		`UPDATE whodunit_commits SET synced_at = ? WHERE repo_id = 'repo'`,
		later.UnixNano()); err != nil {
		t.Fatalf("skew the commits: %v", err)
	}

	got, err := LastSync(db, "repo")
	if err != nil {
		t.Fatalf("LastSync: %v", err)
	}
	if !got.Equal(now) {
		t.Errorf("LastSync = %v, want the repo row's %v — reading the commit "+
			"aggregate (%v) scans every row the repository has", got, now, later)
	}
}

func TestLastSyncAllCoversEveryRepoInOneQuery(t *testing.T) {
	// The cross-repo view asks once for the whole set rather than once per
	// repository: per-repository meant a connection and a 2s timeout each,
	// so ten repositories against a slow target stalled for twenty seconds.
	db := openStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	for _, id := range []string{"repo-a", "repo-b"} {
		p := samplePayload(now)
		p.Repo.RepoID = id
		p.Commits[0].RepoID = id
		p.Commits[0].CommitSHA = "sha-" + id
		p.Events[0].RepoID = id
		p.Events[0].EventID = "ev-" + id
		p.Lines[0].RepoID = id
		if _, err := Write(db, p); err != nil {
			t.Fatalf("Write %s: %v", id, err)
		}
	}

	all, err := LastSyncAll(db)
	if err != nil {
		t.Fatalf("LastSyncAll: %v", err)
	}
	for _, id := range []string{"repo-a", "repo-b"} {
		if got, ok := all[id]; !ok || !got.Equal(now) {
			t.Errorf("LastSyncAll[%s] = %v (present=%v), want %v", id, got, ok, now)
		}
	}

	// A repository that has never published is absent rather than zero, so
	// the caller can tell "never synced" from "synced at the epoch".
	if _, ok := all["never-published"]; ok {
		t.Error("LastSyncAll invented an entry for a repository that never published")
	}
}

// The session pipeline reaches the sidecar intact.
//
// SessionRowsFrom was the one *RowsFrom in this package with no test, and
// sessions are the engagement grain the adoption dashboards read. A field
// dropped here surfaces as a panel that has always shown zero — which reads
// as "the agent did nothing" rather than "the number never arrived".
func TestSessionRowsCarryEveryField(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	first := now.Add(-2 * time.Hour)

	rows := SessionRowsFrom([]journal.Session{{
		Session:       "sess-1",
		Agent:         "claude-code",
		AgentVersion:  "2.1.0",
		FirstSeen:     first,
		LastSeen:      now,
		UserMessages:  7,
		AgentMessages: 11,
		ToolCalls:     23,
		DistinctTools: 5,
		MCPCalls:      3,
	}}, "repo", "dev@example.com", now)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]

	checks := []struct {
		name      string
		got, want any
	}{
		{"RepoID", r.RepoID, "repo"},
		{"Session", r.Session, "sess-1"},
		{"Agent", r.Agent, "claude-code"},
		{"AgentVersion", r.AgentVersion, "2.1.0"},
		{"UserMessages", r.UserMessages, 7},
		{"AgentMessages", r.AgentMessages, 11},
		{"ToolCalls", r.ToolCalls, 23},
		{"DistinctTools", r.DistinctTools, 5},
		{"MCPCalls", r.MCPCalls, 3},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !r.FirstSeen.Equal(first) {
		t.Errorf("FirstSeen = %v, want %v", r.FirstSeen, first)
	}
	if !r.LastSeen.Equal(now) {
		t.Errorf("LastSeen = %v, want %v", r.LastSeen, now)
	}
	if !r.SyncedAt.Equal(now) {
		t.Errorf("SyncedAt = %v, want %v", r.SyncedAt, now)
	}
}

// Sessions survive a write and read back with their counts intact.
func TestSessionsRoundTripThroughTheStore(t *testing.T) {
	db := openStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	p := samplePayload(now)
	p.Sessions = SessionRowsFrom([]journal.Session{{
		Session: "sess-1", Agent: "claude-code", AgentVersion: "2.1.0",
		FirstSeen: now.Add(-time.Hour), LastSeen: now,
		UserMessages: 7, AgentMessages: 11, ToolCalls: 23,
		DistinctTools: 5, MCPCalls: 3,
	}}, "repo", "dev@example.com", now)

	counts, err := Write(db, p)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if counts.Sessions != 1 {
		t.Errorf("wrote %d sessions, want 1", counts.Sessions)
	}

	var toolCalls, mcp int
	if err := db.QueryRow(
		`SELECT tool_calls, mcp_calls FROM whodunit_sessions WHERE session='sess-1'`,
	).Scan(&toolCalls, &mcp); err != nil {
		t.Fatalf("read the session back: %v", err)
	}
	if toolCalls != 23 || mcp != 3 {
		t.Errorf("tool_calls=%d mcp_calls=%d, want 23 and 3", toolCalls, mcp)
	}
}

// Two people syncing the same repository must both survive.
//
// WHO-173, and the measurement the whole WHO-167 epic rests on. repo_id is
// the repository's root commit SHA, identical for everyone who clones it,
// so on the old key of (repo_id) alone the second person's sync overwrote
// the first person's row rather than adding to it.
//
// The damage is not the lost row. whodunit_commits joins to whodunit_repos
// for the contributor, so every commit the first person had already synced
// was silently reattributed to the second. Nothing failed; the dashboards
// rendered a confident wrong answer.
//
// Written through Write rather than raw SQL on purpose: the overwrite is a
// property of the real write path, and a test that inserts directly would
// prove something about SQL rather than about this tool.
func TestTwoContributorsOnOneRepositoryBothSurvive(t *testing.T) {
	db := openStore(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	first := samplePayload(now)
	first.Repo.Contributor = "first@example.com"
	if _, err := Write(db, first); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The same repository, a different person. On a shared database this is
	// the ordinary case, not an edge one.
	second := samplePayload(now)
	second.Repo.Contributor = "second@example.com"
	if _, err := Write(db, second); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM whodunit_repos WHERE repo_id = ?`, "repo").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("got %d row(s) for one repository with two contributors, want 2 "+
			"— the second sync overwrote the first, and every commit already "+
			"synced by the first person is now attributed to the second", n)
	}

	// Both by name, so a test that counts two rows for the wrong reason
	// still fails.
	for _, want := range []string{"first@example.com", "second@example.com"} {
		var got int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM whodunit_repos WHERE repo_id = ? AND contributor = ?`,
			"repo", want).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Errorf("contributor %s has %d row(s), want 1", want, got)
		}
	}
}

// Re-syncing identical data must still add nothing.
//
// WHO-178. Widening the key is only safe if it does not turn a repeated
// sync into a duplicate: the local journal is the source of truth and a
// sync is a projection of it, run on every push.
func TestResyncingTheSameContributorAddsNoRow(t *testing.T) {
	db := openStore(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	p := samplePayload(now)
	p.Repo.Contributor = "same@example.com"
	for i := 0; i < 3; i++ {
		if _, err := Write(db, p); err != nil {
			t.Fatalf("sync %d: %v", i+1, err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM whodunit_repos`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("three identical syncs produced %d row(s), want 1", n)
	}
}

// The point of carrying contributor: filter without a join.
//
// WHO-192. Before this, a per-person panel resolved identity through
// whodunit_repos — the row two people syncing one repository share. The
// filter was therefore only as correct as the row that had last been
// overwritten.
func TestCommitsAndEventsFilterByContributorWithoutAJoin(t *testing.T) {
	db := openStore(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	for _, who := range []string{"first@example.com", "second@example.com"} {
		p := samplePayload(now)
		p.Repo.Contributor = who
		for i := range p.Commits {
			p.Commits[i].Contributor = who
			p.Commits[i].CommitSHA = who[:5] + p.Commits[i].CommitSHA
		}
		for i := range p.Events {
			p.Events[i].Contributor = who
			p.Events[i].EventID = who[:5] + p.Events[i].EventID
		}
		if _, err := Write(db, p); err != nil {
			t.Fatalf("%s: %v", who, err)
		}
	}

	// No whodunit_repos in either query. That is the assertion.
	for _, q := range []struct{ table, sql string }{
		{"whodunit_commits", `SELECT COUNT(*) FROM whodunit_commits WHERE contributor = ?`},
		{"whodunit_events", `SELECT COUNT(*) FROM whodunit_events WHERE contributor = ?`},
	} {
		var n int
		if err := db.QueryRow(q.sql, "first@example.com").Scan(&n); err != nil {
			t.Fatalf("%s: %v", q.table, err)
		}
		if n == 0 {
			t.Errorf("%s has no rows for the first contributor; identity did "+
				"not reach the grain the dashboards query", q.table)
		}

		var other int
		if err := db.QueryRow(q.sql, "second@example.com").Scan(&other); err != nil {
			t.Fatalf("%s: %v", q.table, err)
		}
		if other == 0 {
			t.Errorf("%s has no rows for the second contributor", q.table)
		}
	}
}

// A row synced before the column existed reads as absent, not as a person.
//
// NAV-21. The empty string would be a claim that someone with no name did
// the work; NULL is the honest answer, and a panel renders it as
// unattributed.
func TestAnUnknownContributorIsNullNotEmpty(t *testing.T) {
	db := openStore(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	p := samplePayload(now)
	p.Repo.Contributor = "someone@example.com"
	for i := range p.Commits {
		p.Commits[i].Contributor = "" // as an old row arrives
	}
	if _, err := Write(db, p); err != nil {
		t.Fatal(err)
	}

	var isNull bool
	if err := db.QueryRow(
		`SELECT contributor IS NULL FROM whodunit_commits LIMIT 1`).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("an unknown contributor was stored as the empty string; that " +
			"asserts a person with no name rather than an absent value (NAV-21)")
	}
}
