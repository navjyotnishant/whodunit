package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// The bug this fixes: ParseSessionActivity filtered to response_item and
// dropped everything else, which was 18,142 event_msg and 4,089
// turn_context records across 125 rollouts — around 40% of every
// transcript, and specifically the 40% carrying tokens, timing, model,
// effort and approval policy (NAV-87).
func TestSessionActivityReadsEventMsgAndTurnContext(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.5", "effort": "high", "approval_policy": "on-request",
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input_tokens": 1000, "cached_input_tokens": 400,
					"output_tokens": 200, "reasoning_output_tokens": 50,
					"total_tokens": 1200,
				},
			},
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "task_complete", "duration_ms": 4367, "time_to_first_token_ms": 812,
		}},
	})

	s := parseOne(t, path, ts.Add(-time.Minute))

	if s.Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5 — turn_context is not being read", s.Model)
	}
	if s.Effort != "high" {
		t.Errorf("Effort = %q, want high", s.Effort)
	}
	if s.PermissionMode != "on-request" {
		t.Errorf("PermissionMode = %q, want on-request (Codex calls it approval_policy)", s.PermissionMode)
	}

	// input_tokens is stored as UNCACHED input, matching what Claude Code's
	// field means, so the two agents' columns can be compared. Codex
	// reports the total including cache, so 1000 - 400 = 600.
	assertInt64(t, "InputTokens", s.InputTokens, 600)
	assertInt64(t, "OutputTokens", s.OutputTokens, 200)
	assertInt64(t, "CacheReadTokens", s.CacheReadTokens, 400)
	assertInt64(t, "ReasoningTokens", s.ReasoningTokens, 50)
	assertInt64(t, "DurationMS", s.DurationMS, 4367)
	assertInt64(t, "TimeToFirstTokenMS", s.TimeToFirstTokenMS, 812)
}

// total_token_usage is cumulative for the session, so the LAST record is
// the answer and summing is catastrophically wrong.
//
// Measured on a real 6,562-record rollout: summing the totals gives 1.3
// trillion tokens against a true 423 million — a 3,090x overcount that
// would look merely large on a dashboard rather than obviously broken.
func TestCumulativeTokenTotalsAreNotSummed(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)

	// A cumulative series, with the duplication Codex really emits.
	var lines []map[string]any
	lines = append(lines, map[string]any{
		"timestamp": ts, "type": "session_meta",
		"payload": map[string]any{"id": "s1", "cwd": "/repo", "cli_version": "1.0.0"},
	})
	for _, total := range []int{12014, 12014, 24171, 24171, 36413, 36413} {
		lines = append(lines, map[string]any{
			"timestamp": ts, "type": "event_msg", "payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"total_token_usage": map[string]any{
						"input_tokens": total, "cached_input_tokens": 0,
						"output_tokens": total / 10, "total_tokens": total,
					},
				},
			},
		})
	}

	s := parseOne(t, writeRollout2(t, lines), ts.Add(-time.Minute))

	// The final cumulative value, not the sum. Every field is asserted,
	// not just the first: an earlier version of this test checked only
	// InputTokens, and a mutation that accumulated OutputTokens instead
	// passed it cleanly.
	assertInt64(t, "InputTokens", s.InputTokens, 36413)  // sum would be 145,206
	assertInt64(t, "OutputTokens", s.OutputTokens, 3641) // sum would be 14,520
	assertInt64(t, "CacheReadTokens", s.CacheReadTokens, 0)
}

// Every cumulative field must be overwritten rather than accumulated, and
// the check is generic so a field added later is covered by construction.
//
// Written after a mutation test found the gap: asserting one field by hand
// leaves the others unguarded, and each is a separate opportunity to write
// += instead of =.
func TestNoCumulativeFieldAccumulates(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)

	// Two identical records. Anything that accumulates doubles; anything
	// that overwrites is unchanged. Identical values make the two outcomes
	// impossible to confuse.
	const each = 5000
	usage := map[string]any{
		"input_tokens": each, "cached_input_tokens": 1000,
		"output_tokens": each, "reasoning_output_tokens": each,
		"total_tokens": each * 2,
	}
	lines := []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
	}
	for i := 0; i < 2; i++ {
		lines = append(lines, map[string]any{
			"timestamp": ts, "type": "event_msg", "payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": usage},
			},
		})
	}

	s := parseOne(t, writeRollout2(t, lines), ts.Add(-time.Minute))

	for name, got := range map[string]*int64{
		"InputTokens":     s.InputTokens,     // 5000 - 1000 cached
		"OutputTokens":    s.OutputTokens,    //
		"CacheReadTokens": s.CacheReadTokens, //
		"ReasoningTokens": s.ReasoningTokens, //
	} {
		if got == nil {
			t.Errorf("%s is nil", name)
			continue
		}
		want := int64(each)
		if name == "InputTokens" {
			want = each - 1000
		}
		if name == "CacheReadTokens" {
			want = 1000
		}
		if *got != want {
			t.Errorf("%s = %d after two identical cumulative records, want %d — "+
				"the value is being accumulated rather than overwritten, which on a "+
				"real rollout overcounts by 3,090x", name, *got, want)
		}
	}
}

// NAV-21, on the field that matters most. Codex reports what was read from
// cache but never what was written to it, so CacheWriteTokens must stay
// nil. A 0 would say "nothing was ever cached", and a cache-efficiency
// panel would compute an amortisation ratio against a denominator that was
// never measured.
func TestCacheWriteTokensStayNilBecauseCodexNeverReportsThem(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"total_token_usage": map[string]any{
				"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 5,
			}},
		}},
	}), ts.Add(-time.Minute))

	if s.CacheWriteTokens != nil {
		t.Errorf("CacheWriteTokens = %d, want nil — Codex does not report cache "+
			"writes, and a zero is indistinguishable from 'nothing was cached' "+
			"(NAV-21)", *s.CacheWriteTokens)
	}
}

// A rollout with no telemetry at all must leave every field nil rather
// than zero. This is the shape of an agent that cannot report — and it is
// how agy will always look.
func TestASessionWithNoTelemetryLeavesEveryFieldNil(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "shell",
		}},
	}), ts.Add(-time.Minute))

	for name, v := range map[string]*int64{
		"InputTokens": s.InputTokens, "OutputTokens": s.OutputTokens,
		"CacheReadTokens": s.CacheReadTokens, "CacheWriteTokens": s.CacheWriteTokens,
		"ReasoningTokens": s.ReasoningTokens, "DurationMS": s.DurationMS,
		"TimeToFirstTokenMS": s.TimeToFirstTokenMS,
	} {
		if v != nil {
			t.Errorf("%s = %d for a session reporting nothing; on a cost panel "+
				"that reads as 'this session was free' (NAV-21)", name, *v)
		}
	}
	if s.Model != "" || s.Effort != "" || s.PermissionMode != "" {
		t.Errorf("model/effort/permission set without a turn_context: %q %q %q",
			s.Model, s.Effort, s.PermissionMode)
	}
}

// Real rollouts carry timing on some sessions and tokens on others — both
// partial directions occur, measured at 97 of 125 with timing and 125 of
// 125 with tokens. A partial record must not erase what another supplied.
func TestPartialRecordsDoNotEraseEachOther(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.5", "effort": "high", "approval_policy": "never",
		}},
		// A later turn_context carrying only the model. The effort and
		// policy established above must survive it.
		{"timestamp": ts, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.3-codex",
		}},
		// task_complete with no timing at all — the common shape, most
		// carry only a turn_id.
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "task_complete", "turn_id": "abc",
		}},
	}), ts.Add(-time.Minute))

	if s.Model != "gpt-5.3-codex" {
		t.Errorf("Model = %q, want the last one seen", s.Model)
	}
	if s.Effort != "high" {
		t.Errorf("Effort = %q — a partial turn_context erased it", s.Effort)
	}
	if s.PermissionMode != "never" {
		t.Errorf("PermissionMode = %q — a partial turn_context erased it", s.PermissionMode)
	}
	if s.DurationMS != nil {
		t.Errorf("DurationMS = %d from a task_complete with no timing; "+
			"'instant' is not the same claim as 'unmeasured'", *s.DurationMS)
	}
}

// Codex reports input_tokens including the cached part; the journal's
// column means uncached input, matching Claude Code. A cached count above
// the total would otherwise write a negative, which nothing downstream
// expects.
func TestUncachedInputIsClampedAtZero(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"total_token_usage": map[string]any{
				"input_tokens": 100, "cached_input_tokens": 250,
			}},
		}},
	}), ts.Add(-time.Minute))

	assertInt64(t, "InputTokens", s.InputTokens, 0)
}

// Reading three record types must not change what the original one
// reported. The engagement counts are the existing contract.
func TestReadingMoreRecordTypesDoesNotChangeEngagementCounts(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user",
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "assistant",
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "shell",
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"total_token_usage": map[string]any{"input_tokens": 10}},
		}},
		{"timestamp": ts, "type": "turn_context", "payload": map[string]any{"model": "gpt-5.5"}},
	}), ts.Add(-time.Minute))

	if s.UserMessages != 1 {
		t.Errorf("UserMessages = %d, want 1", s.UserMessages)
	}
	if s.AgentMessages != 1 {
		t.Errorf("AgentMessages = %d, want 1", s.AgentMessages)
	}
	if s.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 — an event_msg or turn_context is being "+
			"counted as a tool call", s.ToolCalls)
	}
}

func parseOne(t *testing.T, path string, since time.Time) journal.Session {
	t.Helper()
	sessions, err := ParseSessionActivity(path, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	return sessions[0]
}

func assertInt64(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is nil, want %d", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func writeRollout2(t *testing.T, lines []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
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

// Widening what is READ must not widen what is STORED (NAV-25).
//
// This is the risk the wider intake creates. The newly-visible records are
// precisely the ones carrying prohibited free text: agent_message.message,
// agent_reasoning.text, turn_context.user_instructions, and
// task_complete.last_agent_message — which sits in the same payload as the
// timing this change reads, one field away.
//
// Distinctive sentinels, checked against the whole rendered session, so a
// leak through any field is caught rather than only the ones anticipated.
func TestNoProhibitedTextReachesTheSession(t *testing.T) {
	const secret = "SENTINEL-cf4b21-prompt-text-must-not-be-stored"

	ts := time.Now().UTC().Add(-time.Hour)
	s := parseOne(t, writeRollout2(t, []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": ts, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.5", "effort": "high", "approval_policy": "never",
			"user_instructions":      secret,
			"developer_instructions": secret,
			"base_instructions":      secret,
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "agent_message", "message": secret,
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "agent_reasoning", "text": secret,
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "user_message", "message": secret,
		}},
		// The dangerous one: last_agent_message shares a payload with the
		// timing fields this change reads.
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "task_complete", "duration_ms": 100,
			"last_agent_message": secret,
		}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{
			"type": "web_search_end", "query": secret,
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "shell", "arguments": secret,
		}},
	}), ts.Add(-time.Minute))

	rendered, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), secret) {
		t.Errorf("prompt text reached the session record (NAV-25):\n%s", rendered)
	}

	// And the telemetry it SHOULD have taken from those same records is
	// still there — a test that passes by reading nothing proves nothing.
	if s.Model != "gpt-5.5" {
		t.Errorf("Model = %q; the test is passing because nothing was read at all", s.Model)
	}
	assertInt64(t, "DurationMS", s.DurationMS, 100)
}

// A session whose every record predates the cutoff must yield nothing, not
// an empty row.
//
// The rollout's session_meta is read before the cutoff is applied, so the
// session id exists regardless of the window. Without an explicit check,
// `dun ingest --since` over a recent window wrote one row per historical
// rollout: every counter zero, and FirstSeen the zero time, which reaches
// SQLite as -6795364578871345152.
//
// Found by running the real pipeline against a real repository rather than
// by reading the code — 74 Codex sessions written, every one empty. Those
// rows then take part in averages, so a per-session token average is
// divided by a denominator mostly made of sessions nobody read.
func TestASessionEntirelyBeforeTheCutoffIsNotWritten(t *testing.T) {
	old := time.Now().UTC().Add(-48 * time.Hour)
	path := writeRollout2(t, []map[string]any{
		{"timestamp": old, "type": "session_meta", "payload": map[string]any{
			"id": "s-old", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": old, "type": "event_msg", "payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"total_token_usage": map[string]any{"input_tokens": 999}},
		}},
	})

	sessions, err := ParseSessionActivity(path, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions for a rollout entirely before the cutoff, want 0 "+
			"(FirstSeen=%v, ToolCalls=%d) — an empty row still counts in every "+
			"average computed over sessions",
			len(sessions), sessions[0].FirstSeen, sessions[0].ToolCalls)
	}
}

// The other direction: a session partly inside the window is still read,
// over the part that is visible.
func TestASessionPartlyInsideTheWindowIsStillRead(t *testing.T) {
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-30 * time.Minute)

	path := writeRollout2(t, []map[string]any{
		{"timestamp": old, "type": "session_meta", "payload": map[string]any{
			"id": "s1", "cwd": "/repo", "cli_version": "1.0.0",
		}},
		{"timestamp": recent, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.5",
		}},
	})

	sessions, err := ParseSessionActivity(path, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5", sessions[0].Model)
	}
}
