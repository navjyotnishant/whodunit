package agy

import (
	"testing"
	"time"
)

// agy reports no token usage, no per-turn timing and no reasoning split.
// Every one of those fields must stay nil rather than becoming zero
// (NAV-95).
//
// The distinction is the difference between "this agent is free and
// instantaneous" and "this agent does not tell us what it cost". A cost
// panel showing 0 makes the first claim, and nothing downstream can
// recover the truth once the row is written.
//
// This is asserted rather than left to the adapter's silence, because
// nothing about `return journal.Session{...}` makes an omitted field
// visible — the failure mode is someone adding a well-meaning `0` to make
// a column "complete".
func TestAgySuppliesNoTokensOrTiming(t *testing.T) {
	sessions, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions; the test cannot prove anything")
	}

	for _, s := range sessions {
		for name, v := range map[string]*int64{
			"InputTokens":        s.InputTokens,
			"OutputTokens":       s.OutputTokens,
			"CacheReadTokens":    s.CacheReadTokens,
			"CacheWriteTokens":   s.CacheWriteTokens,
			"ReasoningTokens":    s.ReasoningTokens,
			"DurationMS":         s.DurationMS,
			"TimeToFirstTokenMS": s.TimeToFirstTokenMS,
		} {
			if v != nil {
				t.Errorf("%s = %d, want nil — agy records none of this, and a zero "+
					"reports an agent that is free and instantaneous (NAV-21)",
					name, *v)
			}
		}

		// Effort and permission mode are the same claim in string form.
		if s.Effort != "" {
			t.Errorf("Effort = %q, want empty — agy does not report it", s.Effort)
		}
		if s.PermissionMode != "" {
			t.Errorf("PermissionMode = %q, want empty", s.PermissionMode)
		}
	}
}

// The model is the one thing agy does report, so the test above must not
// be passing because the adapter returns nothing useful at all.
func TestAgyStillReportsWhatItHas(t *testing.T) {
	sessions, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]

	if s.ToolCalls == 0 {
		t.Error("ToolCalls = 0; the adapter is returning an empty session and the " +
			"absence assertions above prove nothing")
	}
	if s.DistinctTools == 0 {
		t.Error("DistinctTools = 0")
	}
	if s.Agent != AgentName {
		t.Errorf("Agent = %q, want %q", s.Agent, AgentName)
	}
}

// agy stores no per-message record this adapter can distinguish, so the
// message counts are a real zero rather than a measurement.
//
// Worth its own assertion because it is the one place agy uses 0 to mean
// "not measurable" — an int cannot carry nil, and changing the type for
// one agent would ripple through every adapter. The dashboard shows
// tool_calls alongside, which is the denominator that makes the zero
// readable.
func TestAgyMessageCountsAreZeroByConstruction(t *testing.T) {
	sessions, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	s := sessions[0]

	if s.UserMessages != 0 || s.AgentMessages != 0 {
		t.Errorf("message counts are %d/%d; agy has no per-message record, so a "+
			"non-zero here means something is being guessed at",
			s.UserMessages, s.AgentMessages)
	}
}

// agy has no compaction signal of any kind, so it stays nil (NAV-106).
//
// This is the case where nil is right and zero would be wrong — the
// inverse of the other two adapters. There, a parsed transcript with no
// boundary is a measured zero. Here there is nothing to measure at all:
// agy records no context management, so reporting 0 would assert that agy
// sessions never compact, when the truth is that we cannot see whether
// they do.
func TestAgyReportsNoCompactions(t *testing.T) {
	sessions, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions")
	}
	for _, s := range sessions {
		if s.Compactions != nil {
			t.Errorf("Compactions = %d, want nil — agy records no context "+
				"management, so a zero would claim its sessions never compact "+
				"when we simply cannot see (NAV-21)", *s.Compactions)
		}
	}
}
