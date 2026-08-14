package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Claude Code reports usage PER TURN and the totals are summed — the exact
// opposite of Codex, whose total_token_usage is cumulative and must be
// overwritten. Getting either backwards is the expensive mistake here, so
// both are asserted in their own package rather than trusted to a comment.
func TestTokensAreSummedAcrossTurns(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 5000, 300),
		assistantTurn(ts, "s1", "msg-2", "claude-opus-5", 150, 30, 6000, 0),
	)

	s := oneSession(t, path)

	assertTokens(t, "InputTokens", s.InputTokens, 250)
	assertTokens(t, "OutputTokens", s.OutputTokens, 50)
	assertTokens(t, "CacheReadTokens", s.CacheReadTokens, 11000)
	assertTokens(t, "CacheWriteTokens", s.CacheWriteTokens, 300)
}

// One assistant message is written as SEVERAL records — one per content
// block — each repeating the same id and the same usage. Summing per
// record rather than per message inflates every token count.
//
// Measured on the largest transcript on this machine: 12,029 usage-bearing
// records for 6,687 distinct messages, a 1.93x overcount. Large enough to
// matter, small enough to look plausible on a dashboard.
func TestRepeatedRecordsOfOneMessageAreCountedOnce(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)

	// The same message id three times, as a message with three content
	// blocks really appears.
	turn := assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 5000, 300)
	s := oneSession(t, writeUsageTranscript(t, turn, turn, turn))

	assertTokens(t, "InputTokens", s.InputTokens, 100)
	assertTokens(t, "OutputTokens", s.OutputTokens, 20)
	assertTokens(t, "CacheReadTokens", s.CacheReadTokens, 5000)
	assertTokens(t, "CacheWriteTokens", s.CacheWriteTokens, 300)

	// The same deduplication fixes AgentMessages, which counted records.
	// Codex already counted messages, so the column meant different things
	// depending on which agent filled it — and nothing on a dashboard
	// showing both would reveal that.
	if s.AgentMessages != 1 {
		t.Errorf("AgentMessages = %d for one message written as three records, "+
			"want 1; Codex counts messages, so this column would not be "+
			"comparable across agents", s.AgentMessages)
	}
}

// A record with no message id cannot be deduplicated. Counting it is the
// safe direction: dropping it would undercount, and an undercount of cost
// is worse than a duplicate, since it understates rather than merely
// confuses.
func TestRecordsWithoutAMessageIDAreStillCounted(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := oneSession(t, writeUsageTranscript(t,
		assistantTurn(ts, "s1", "", "claude-opus-5", 100, 20, 0, 0),
		assistantTurn(ts, "s1", "", "claude-opus-5", 100, 20, 0, 0),
	))

	assertTokens(t, "InputTokens", s.InputTokens, 200)
	if s.AgentMessages != 2 {
		t.Errorf("AgentMessages = %d, want 2", s.AgentMessages)
	}
}

// NAV-21. A transcript with no assistant turn at all — opened and
// abandoned — must leave every token field nil rather than a row of
// zeroes. Zero is a measurement; nil is the absence of one.
func TestATranscriptWithNoAssistantTurnsReportsNilNotZero(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t, map[string]any{
		"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
		"message": map[string]any{"content": []any{}},
	})

	s := oneSession(t, path)

	for name, v := range map[string]*int64{
		"InputTokens": s.InputTokens, "OutputTokens": s.OutputTokens,
		"CacheReadTokens": s.CacheReadTokens, "CacheWriteTokens": s.CacheWriteTokens,
	} {
		if v != nil {
			t.Errorf("%s = %d for a session with no assistant turn; on a cost "+
				"panel a zero reads as 'this session was free' (NAV-21)", name, *v)
		}
	}
}

// Claude Code records no per-turn timing and does not separate reasoning
// tokens. Those must stay nil rather than zero — "not reported" is not
// "instantaneous", and a latency panel averaging in zeroes would report
// this agent as the fastest of the three.
func TestFieldsClaudeCodeDoesNotReportStayNil(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := oneSession(t, writeUsageTranscript(t,
		assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 0, 0),
	))

	if s.ReasoningTokens != nil {
		t.Errorf("ReasoningTokens = %d, want nil — Claude Code does not separate "+
			"them (NAV-21)", *s.ReasoningTokens)
	}
	if s.DurationMS != nil {
		t.Errorf("DurationMS = %d, want nil — Claude Code records no timing, and "+
			"a zero would make it the fastest agent on any latency panel",
			*s.DurationMS)
	}
	if s.TimeToFirstTokenMS != nil {
		t.Errorf("TimeToFirstTokenMS = %d, want nil", *s.TimeToFirstTokenMS)
	}
}

// <synthetic> is the sender on records Claude Code generates itself rather
// than receiving from a model. It is not a model, and left in it becomes a
// row on every per-model panel carrying no tokens — and the cheapest series
// in any ratio, making every comparison against it infinite.
func TestSyntheticSenderIsNotRecordedAsAModel(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := oneSession(t, writeUsageTranscript(t,
		assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 0, 0),
		assistantTurn(ts, "s1", "msg-2", "<synthetic>", 0, 0, 0, 0),
	))

	if s.Model == "<synthetic>" {
		t.Error("Model is <synthetic>, which is not a model — it would appear as " +
			"a row on every per-model panel with no tokens against it")
	}
	if s.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want the last real model seen", s.Model)
	}
}

// The model recorded is the last real one seen. A session that starts on a
// small model and escalates is better described by what it escalated to —
// that is the turn that finished the work.
func TestTheLastModelSeenWins(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := oneSession(t, writeUsageTranscript(t,
		assistantTurn(ts, "s1", "msg-1", "claude-haiku-4-5", 10, 5, 0, 0),
		assistantTurn(ts, "s1", "msg-2", "claude-opus-5", 100, 20, 0, 0),
	))

	if s.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want claude-opus-5", s.Model)
	}
}

// permissionMode is written on USER records, not assistant ones — measured,
// not assumed. Reading it off the assistant turn finds nothing at all, and
// the field would silently stay empty forever.
func TestPermissionModeIsReadFromUserRecords(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"permissionMode": "bypassPermissions",
			"message":        map[string]any{"content": []any{}},
		},
		assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 0, 0),
	)

	s := oneSession(t, path)
	if s.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q, want bypassPermissions — it is recorded on "+
			"user records, so reading only assistant turns finds nothing",
			s.PermissionMode)
	}
}

// Tokens are per session, and one transcript file holds several sessions.
// Attributing them all to one would make a busy file look like a single
// enormously expensive session.
func TestTokensAreAttributedPerSession(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		assistantTurn(ts, "s1", "msg-1", "claude-opus-5", 100, 20, 0, 0),
		assistantTurn(ts, "s2", "msg-2", "claude-sonnet-5", 900, 80, 0, 0),
	)

	sessions, err := ParseSessionActivity(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}

	byID := map[string]journal.Session{}
	for _, s := range sessions {
		byID[s.Session] = s
	}
	assertTokens(t, "s1 InputTokens", byID["s1"].InputTokens, 100)
	assertTokens(t, "s2 InputTokens", byID["s2"].InputTokens, 900)
	if byID["s2"].Model != "claude-sonnet-5" {
		t.Errorf("s2 model = %q, want claude-sonnet-5", byID["s2"].Model)
	}
}

// Reading usage must not change what the adapter already reported. Tool
// calls and user messages are the existing contract.
func TestReadingUsageDoesNotChangeExistingCounts(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"message": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "hello"},
			}},
		},
		map[string]any{
			"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"message": map[string]any{
				"id": "msg-1", "model": "claude-opus-5",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
				"content": []any{
					map[string]any{"type": "tool_use", "id": "t1", "name": "Bash",
						"input": map[string]any{}},
				},
			},
		},
	)

	s := oneSession(t, path)
	if s.UserMessages != 1 {
		t.Errorf("UserMessages = %d, want 1", s.UserMessages)
	}
	if s.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", s.ToolCalls)
	}
	if s.AgentMessages != 1 {
		t.Errorf("AgentMessages = %d, want 1", s.AgentMessages)
	}
}

// No prompt text may reach the session record. Usage sits on the same
// message object as content, one field away (NAV-25).
func TestNoPromptTextReachesTheSession(t *testing.T) {
	const secret = "SENTINEL-9f31ac-prompt-text-must-not-be-stored"

	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t, map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
		"message": map[string]any{
			"id": "msg-1", "model": "claude-opus-5",
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
			"content": []any{
				map[string]any{"type": "text", "text": secret},
				map[string]any{"type": "tool_use", "id": "t1", "name": "Bash",
					"input": map[string]any{"command": secret}},
			},
		},
	})

	s := oneSession(t, path)
	rendered, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), secret) {
		t.Errorf("prompt text reached the session record (NAV-25):\n%s", rendered)
	}
	// Not passing by reading nothing.
	assertTokens(t, "InputTokens", s.InputTokens, 10)
}

func assistantTurn(ts time.Time, session, msgID, model string, in, out, cacheRead, cacheWrite int64) map[string]any {
	return map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": session, "version": "2.1.0",
		"message": map[string]any{
			"id": msgID, "model": model,
			"usage": map[string]any{
				"input_tokens": in, "output_tokens": out,
				"cache_read_input_tokens": cacheRead, "cache_creation_input_tokens": cacheWrite,
			},
			"content": []any{},
		},
	}
}

func oneSession(t *testing.T, path string) journal.Session {
	t.Helper()
	sessions, err := ParseSessionActivity(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	return sessions[0]
}

func assertTokens(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is nil, want %d", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func writeUsageTranscript(t *testing.T, lines ...map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
