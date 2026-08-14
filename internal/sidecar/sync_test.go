package sidecar

import (
	"path/filepath"
	"testing"
	"time"
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
	if len(stmts) != 5 {
		t.Fatalf("want 5 statements for 5 tables, got %d", len(stmts))
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
