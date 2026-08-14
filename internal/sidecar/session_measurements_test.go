package sidecar

import (
	"database/sql"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

func i64(v int64) *int64 { return &v }

// The whole point of wiring the measurements through: a value read from a
// transcript must survive the journal, the sync and the central store
// unchanged. Every layer preserves nil separately, so every layer is a
// separate opportunity to turn it into zero.
func TestMeasurementsSurviveTheSync(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	rows := SessionRowsFrom([]journal.Session{{
		Session: "s1", Agent: "claude-code", AgentVersion: "2.1.0",
		FirstSeen: now, LastSeen: now,
		UserMessages: 3, AgentMessages: 7,

		InputTokens: i64(1000), OutputTokens: i64(200),
		CacheReadTokens: i64(50000), CacheWriteTokens: i64(3000),
		Effort: "high", PermissionMode: "bypassPermissions",
		Model: "claude-opus-5",
		// Claude Code reports no timing and no reasoning split.
	}}, "repo-1", now)

	if _, err := Write(store, Payload{
		Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
		Sessions: rows,
	}); err != nil {
		t.Fatal(err)
	}

	got := readSession(t, db, "s1")

	assertNullInt(t, "input_tokens", got.inputTokens, 1000)
	assertNullInt(t, "output_tokens", got.outputTokens, 200)
	assertNullInt(t, "cache_read_tokens", got.cacheRead, 50000)
	assertNullInt(t, "cache_write_tokens", got.cacheWrite, 3000)

	if got.model.String != "claude-opus-5" {
		t.Errorf("model = %q, want claude-opus-5", got.model.String)
	}
	if got.effort.String != "high" {
		t.Errorf("effort = %q, want high", got.effort.String)
	}

	// The fields this agent cannot report must be NULL centrally, not 0.
	// A latency panel averaging in zeroes would report Claude Code as the
	// fastest of the three agents (NAV-21).
	if got.durationMS.Valid {
		t.Errorf("duration_ms = %d, want NULL — Claude Code records no timing",
			got.durationMS.Int64)
	}
	if got.reasoning.Valid {
		t.Errorf("reasoning_tokens = %d, want NULL — Claude Code does not separate them",
			got.reasoning.Int64)
	}
}

// A re-sync that carries no measurements must not erase the ones already
// stored.
//
// The realistic path: `dun ingest --since` reads a narrow window, a
// session's token-bearing turns fall outside it, and the session parses
// with nil tokens. A plain overwrite would replace real figures with NULL
// centrally — where the original transcript is not available to re-read,
// so the loss is permanent as well as silent.
func TestAReSyncWithoutMeasurementsDoesNotEraseThem(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	base := journal.Session{
		Session: "s1", Agent: "claude-code", FirstSeen: now, LastSeen: now,
		AgentMessages: 7,
		InputTokens:   i64(1000), OutputTokens: i64(200),
		Model: "claude-opus-5", Effort: "high",
	}
	write := func(s journal.Session) {
		t.Helper()
		if _, err := Write(store, Payload{
			Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
			Sessions: SessionRowsFrom([]journal.Session{s}, "repo-1", now),
		}); err != nil {
			t.Fatal(err)
		}
	}

	write(base)

	// The same session, re-read over a window that saw no usage records.
	// Counters move; measurements are absent.
	narrow := base
	narrow.InputTokens, narrow.OutputTokens = nil, nil
	narrow.Model, narrow.Effort = "", ""
	narrow.AgentMessages = 9
	write(narrow)

	got := readSession(t, db, "s1")

	assertNullInt(t, "input_tokens", got.inputTokens, 1000)
	assertNullInt(t, "output_tokens", got.outputTokens, 200)
	if got.model.String != "claude-opus-5" {
		t.Errorf("model = %q; a re-sync with no model erased it", got.model.String)
	}
	if got.effort.String != "high" {
		t.Errorf("effort = %q; a re-sync with no effort erased it", got.effort.String)
	}

	// The counters ARE meant to be replaced: they describe whatever window
	// was read, and the newest read is the right one.
	if got.agentMessages != 9 {
		t.Errorf("agent_messages = %d, want 9 — counters should be refreshed, "+
			"only measurements are preserved", got.agentMessages)
	}
}

// The mirror: a later sync that DOES carry a measurement must update it.
// COALESCE preserving nil must not become COALESCE ignoring new values.
func TestALaterMeasurementReplacesAnEarlierOne(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	write := func(s journal.Session) {
		t.Helper()
		if _, err := Write(store, Payload{
			Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
			Sessions: SessionRowsFrom([]journal.Session{s}, "repo-1", now),
		}); err != nil {
			t.Fatal(err)
		}
	}

	write(journal.Session{
		Session: "s1", Agent: "claude-code", FirstSeen: now, LastSeen: now,
		InputTokens: i64(1000), Model: "claude-sonnet-5",
	})
	// The session continued and cost more, and escalated model.
	write(journal.Session{
		Session: "s1", Agent: "claude-code", FirstSeen: now, LastSeen: now,
		InputTokens: i64(4000), Model: "claude-opus-5",
	})

	got := readSession(t, db, "s1")
	assertNullInt(t, "input_tokens", got.inputTokens, 4000)
	if got.model.String != "claude-opus-5" {
		t.Errorf("model = %q, want claude-opus-5 — a growing session must update",
			got.model.String)
	}
}

// An agent that reports nothing must produce NULLs rather than a row of
// zeroes. This is the shape every agy session will have.
func TestAnAgentThatReportsNothingStoresNulls(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if _, err := Write(store, Payload{
		Repo: RepoRow{RepoID: "repo-1", SyncedAt: now},
		Sessions: SessionRowsFrom([]journal.Session{{
			Session: "s1", Agent: "agy", FirstSeen: now, LastSeen: now,
			ToolCalls: 12,
		}}, "repo-1", now),
	}); err != nil {
		t.Fatal(err)
	}

	got := readSession(t, db, "s1")
	for name, v := range map[string]sql.NullInt64{
		"input_tokens": got.inputTokens, "output_tokens": got.outputTokens,
		"cache_read_tokens": got.cacheRead, "cache_write_tokens": got.cacheWrite,
		"reasoning_tokens": got.reasoning, "duration_ms": got.durationMS,
	} {
		if v.Valid {
			t.Errorf("%s = %d for an agent that reports nothing; on a cost panel "+
				"a zero reads as 'this agent is free' (NAV-21)", name, v.Int64)
		}
	}
	if got.model.Valid {
		t.Errorf("model = %q, want NULL", got.model.String)
	}
}

// An empty string must reach the column as NULL, not as ”. Written as ”,
// COALESCE would treat it as a real value and refuse to let a later sync
// fill it in — the preservation rule turning into a poison pill.
func TestEmptyStringsAreStoredAsNull(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	write := func(model string) {
		t.Helper()
		if _, err := Write(store, Payload{
			Repo: RepoRow{RepoID: "repo-1", SyncedAt: now},
			Sessions: SessionRowsFrom([]journal.Session{{
				Session: "s1", Agent: "codex", FirstSeen: now, LastSeen: now,
				Model: model,
			}}, "repo-1", now),
		}); err != nil {
			t.Fatal(err)
		}
	}

	write("") // first sync saw no model
	if got := readSession(t, db, "s1"); got.model.Valid {
		t.Fatalf("model stored as %q, want NULL — an empty string would block a "+
			"later sync from filling it in", got.model.String)
	}

	write("gpt-5.5") // a later one did
	if got := readSession(t, db, "s1"); got.model.String != "gpt-5.5" {
		t.Errorf("model = %q, want gpt-5.5 — an empty string blocked the update",
			got.model.String)
	}
}

type storedSession struct {
	agentMessages                                    int
	inputTokens, outputTokens, cacheRead, cacheWrite sql.NullInt64
	reasoning, durationMS, ttft                      sql.NullInt64
	effort, permission, model                        sql.NullString
}

func readSession(t *testing.T, db *sql.DB, session string) storedSession {
	t.Helper()
	var s storedSession
	err := db.QueryRow(`SELECT agent_messages,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		reasoning_tokens, duration_ms, time_to_first_token_ms,
		effort, permission_mode, model
		FROM whodunit_sessions WHERE session = ?`, session).
		Scan(&s.agentMessages, &s.inputTokens, &s.outputTokens, &s.cacheRead,
			&s.cacheWrite, &s.reasoning, &s.durationMS, &s.ttft,
			&s.effort, &s.permission, &s.model)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertNullInt(t *testing.T, name string, got sql.NullInt64, want int64) {
	t.Helper()
	if !got.Valid {
		t.Errorf("%s is NULL, want %d", name, want)
		return
	}
	if got.Int64 != want {
		t.Errorf("%s = %d, want %d", name, got.Int64, want)
	}
}

// The per-entry observations must survive the sync too (NAV-92), and an
// agent that cannot supply one must store NULL rather than ”.
func TestEntryObservationsSurviveTheSync(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	modified := true
	write := func(e journal.Entry) {
		t.Helper()
		if _, err := Write(store, Payload{
			Repo:   RepoRow{RepoID: "repo-1", SyncedAt: now},
			Events: EventRowsFrom([]journal.Entry{e}, "repo-1", now),
		}); err != nil {
			t.Fatal(err)
		}
	}

	write(journal.Entry{
		Timestamp: now, Agent: "claude-code", Session: "s1", Event: "tool_use",
		Tool: "Write", File: "/repo/x.go",
		Model: "claude-opus-5", Branch: "main", MCPServer: "linear-server",
		UserModified: &modified,
	})

	var model, branch, mcp sql.NullString
	var userModified sql.NullBool
	if err := db.QueryRow(`SELECT model, branch, mcp_server, user_modified
		FROM whodunit_events`).Scan(&model, &branch, &mcp, &userModified); err != nil {
		t.Fatal(err)
	}

	if model.String != "claude-opus-5" || branch.String != "main" || mcp.String != "linear-server" {
		t.Errorf("observations lost: model=%q branch=%q mcp=%q",
			model.String, branch.String, mcp.String)
	}
	if !userModified.Valid || !userModified.Bool {
		t.Errorf("user_modified = %v/%v, want true", userModified.Valid, userModified.Bool)
	}
}

// agy supplies no branch, no MCP server and no edit signal. Those must be
// NULL centrally, not empty strings or false — false asserts that nobody
// edited the output (NAV-21).
func TestAnAgentWithNoBranchStoresNull(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if _, err := Write(store, Payload{
		Repo: RepoRow{RepoID: "repo-1", SyncedAt: now},
		Events: EventRowsFrom([]journal.Entry{{
			Timestamp: now, Agent: "agy", Session: "s1", Event: "tool_use",
			Tool: "write_file", File: "/repo/x.go",
			Model: "gemini-3.7-flash-high",
		}}, "repo-1", now),
	}); err != nil {
		t.Fatal(err)
	}

	var branch, mcp sql.NullString
	var userModified sql.NullBool
	if err := db.QueryRow(`SELECT branch, mcp_server, user_modified
		FROM whodunit_events`).Scan(&branch, &mcp, &userModified); err != nil {
		t.Fatal(err)
	}
	if branch.Valid {
		t.Errorf("branch = %q, want NULL — agy records none", branch.String)
	}
	if mcp.Valid {
		t.Errorf("mcp_server = %q, want NULL", mcp.String)
	}
	if userModified.Valid {
		t.Errorf("user_modified = %v, want NULL — agy has no such signal", userModified.Bool)
	}
}
