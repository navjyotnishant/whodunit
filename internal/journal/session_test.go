// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: The session engagement pipeline (NAV-55), end to end.

package journal

import (
	"strings"
	"testing"
	"time"
)

// Sessions are the engagement grain: how much conversation and tool use a
// session held. The whole path — UpsertSession, ReadSessions, and the
// sidecar rows built from them — had no test, so a field dropped or
// mis-assigned anywhere along it would show up as zeros on a dashboard.
//
// Zeros are the failure mode that matters: they read as "the agent did
// nothing" rather than "the number never arrived", which is NAV-21 applied
// to engagement.

func sampleSession(id string, now time.Time) Session {
	return Session{
		Session:       id,
		Agent:         "claude-code",
		AgentVersion:  "2.1.0",
		FirstSeen:     now.Add(-time.Hour),
		LastSeen:      now,
		UserMessages:  7,
		AgentMessages: 11,
		ToolCalls:     23,
		DistinctTools: 5,
		MCPCalls:      3,
	}
}

func TestSessionRoundTripKeepsEveryField(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)

	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	want := sampleSession("sess-1", now)
	if err := w.UpsertSession(want); err != nil {
		t.Fatal(err)
	}
	w.Close()

	got, err := ReadSessions(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d sessions, want 1", len(got))
	}

	// Every field, not a sample of them: a dropped column is invisible
	// until someone reads a dashboard panel that has always shown zero.
	g := got[0]
	if g.Session != want.Session {
		t.Errorf("Session = %q, want %q", g.Session, want.Session)
	}
	if g.Agent != want.Agent {
		t.Errorf("Agent = %q, want %q", g.Agent, want.Agent)
	}
	if g.AgentVersion != want.AgentVersion {
		t.Errorf("AgentVersion = %q, want %q", g.AgentVersion, want.AgentVersion)
	}
	if !g.FirstSeen.Equal(want.FirstSeen) {
		t.Errorf("FirstSeen = %v, want %v", g.FirstSeen, want.FirstSeen)
	}
	if !g.LastSeen.Equal(want.LastSeen) {
		t.Errorf("LastSeen = %v, want %v", g.LastSeen, want.LastSeen)
	}
	if g.UserMessages != want.UserMessages {
		t.Errorf("UserMessages = %d, want %d", g.UserMessages, want.UserMessages)
	}
	if g.AgentMessages != want.AgentMessages {
		t.Errorf("AgentMessages = %d, want %d", g.AgentMessages, want.AgentMessages)
	}
	if g.ToolCalls != want.ToolCalls {
		t.Errorf("ToolCalls = %d, want %d", g.ToolCalls, want.ToolCalls)
	}
	if g.DistinctTools != want.DistinctTools {
		t.Errorf("DistinctTools = %d, want %d", g.DistinctTools, want.DistinctTools)
	}
	if g.MCPCalls != want.MCPCalls {
		t.Errorf("MCPCalls = %d, want %d", g.MCPCalls, want.MCPCalls)
	}
}

// A session is re-upserted every time its transcript is re-read, which the
// commit hook does on every commit. The counts must reflect the latest read
// rather than accumulating.
func TestUpsertSessionReplacesRatherThanAccumulates(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)

	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	first := sampleSession("sess-1", now)
	if err := w.UpsertSession(first); err != nil {
		t.Fatal(err)
	}

	// The session continued: more messages, more tools.
	second := first
	second.UserMessages = 9
	second.ToolCalls = 40
	second.LastSeen = now.Add(time.Hour)
	if err := w.UpsertSession(second); err != nil {
		t.Fatal(err)
	}

	got, err := ReadSessions(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d sessions after two upserts of one session, want 1", len(got))
	}
	if got[0].ToolCalls != 40 {
		t.Errorf("ToolCalls = %d, want 40: an upsert must replace the counts, "+
			"not add to them — the hook re-reads the whole transcript every "+
			"commit, so accumulating would multiply every number by the "+
			"number of commits", got[0].ToolCalls)
	}
	if !got[0].LastSeen.Equal(now.Add(time.Hour)) {
		t.Errorf("LastSeen = %v, want the later time", got[0].LastSeen)
	}
}

// Sessions are scoped by repository like everything else here.
func TestSessionsAreScopedByRepo(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC()

	for _, repo := range []string{"repo-a", "repo-b"} {
		w, err := NewWriter(dataDir, repo)
		if err != nil {
			t.Fatal(err)
		}
		s := sampleSession("shared-session-id", now)
		if err := w.UpsertSession(s); err != nil {
			t.Fatal(err)
		}
		w.Close()
	}

	for _, repo := range []string{"repo-a", "repo-b"} {
		got, err := ReadSessions(dataDir, repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Errorf("%s sees %d sessions, want its own 1", repo, len(got))
		}
	}
}

// A session with no id is dropped rather than stored under an empty key.
//
// Silent, and correct — an empty id belongs to no session and would collide
// with every other empty one — but worth pinning, because the alternative
// implementation (store it anyway) merges unrelated sessions into one row.
func TestASessionWithNoIDIsNotStored(t *testing.T) {
	dataDir := t.TempDir()

	w, err := NewWriter(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	s := sampleSession("", time.Now())
	if err := w.UpsertSession(s); err != nil {
		t.Fatalf("UpsertSession with no id returned an error: %v", err)
	}

	got, err := ReadSessions(dataDir, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a session with no id was stored: %+v", got)
	}
}

// The journal must never hold conversation content — only counts.
//
// NAV-25: a message count needs no message text. This asserts the schema
// itself, so a column added later to carry a prompt fails here rather than
// in review.
func TestTheSessionsTableHoldsNoContent(t *testing.T) {
	dataDir := t.TempDir()
	db, err := open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM pragma_table_info('sessions')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	forbidden := []string{"message", "text", "content", "prompt", "body", "summary"}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatal(err)
		}
		for _, bad := range forbidden {
			// user_messages and agent_messages are counts, and end in "s".
			if strings.Contains(col, bad) && !strings.HasSuffix(col, "_messages") {
				t.Errorf("the sessions table has a column %q; this table holds "+
					"counts only, never conversation content (NAV-25)", col)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
