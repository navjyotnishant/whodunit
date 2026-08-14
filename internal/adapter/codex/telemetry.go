// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Reading tokens, timing, model and autonomy out of Codex's
// event_msg and turn_context records.

package codex

import (
	"encoding/json"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// tokenUsage is one usage snapshot. Codex reports two of these per record
// and they mean different things — see readEventMsg.
type tokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type eventMsg struct {
	Type string `json:"type"`

	// token_count
	Info *struct {
		TotalTokenUsage *tokenUsage `json:"total_token_usage"`
		LastTokenUsage  *tokenUsage `json:"last_token_usage"`
	} `json:"info"`

	// task_complete
	DurationMS         *int64 `json:"duration_ms"`
	TimeToFirstTokenMS *int64 `json:"time_to_first_token_ms"`
}

// readEventMsg takes token counts and turn timing off an event_msg record.
//
// **total_token_usage is cumulative for the session, not per record.**
// That is the one thing to get right here, and getting it wrong is not a
// small error: summing the totals across a real 6,562-record rollout
// measured on this machine gives 1.3 trillion tokens against a true
// 423 million — a 3,090x overcount, and one that would look plausible on
// a dashboard because it is merely a large number.
//
// So the last record wins rather than accumulating. Codex also emits the
// same snapshot more than once (the series runs 12014, 12014, 24171,
// 24171, …), which is why summing last_token_usage is wrong too: it gives
// 860M against the same true 423M. Overwriting is immune to both.
//
// A record arriving out of order would overwrite a later total with an
// earlier one. Not guarded, because the file is append-only and read in
// order — noted so the assumption is visible rather than accidental.
func readEventMsg(s *journal.Session, payload json.RawMessage) {
	var e eventMsg
	if err := json.Unmarshal(payload, &e); err != nil {
		return
	}

	switch e.Type {
	case "token_count":
		if e.Info == nil || e.Info.TotalTokenUsage == nil {
			return
		}
		u := e.Info.TotalTokenUsage

		// Codex reports input_tokens as the whole input including the
		// cached part, while the journal's input_tokens means uncached
		// input — the same meaning Claude Code's field carries, so the two
		// agents' columns can be compared at all. Subtracting here rather
		// than at the query keeps that difference in one place.
		//
		// Clamped: a cached count above the total would otherwise write a
		// negative, and a negative token count is worse than a wrong one
		// because nothing downstream expects it.
		uncached := u.InputTokens - u.CachedInputTokens
		if uncached < 0 {
			uncached = 0
		}
		s.InputTokens = int64p(uncached)
		s.OutputTokens = int64p(u.OutputTokens)
		s.CacheReadTokens = int64p(u.CachedInputTokens)
		s.ReasoningTokens = int64p(u.ReasoningOutputTokens)

		// Deliberately not set: Codex reports what was read from cache but
		// never what was written to it. Leaving CacheWriteTokens nil says
		// "not reported"; a 0 would say "nothing was ever cached", and a
		// cache-efficiency panel would compute an amortisation ratio
		// against a denominator that was never measured (NAV-21).

	case "task_complete":
		// The only agent of the three that records timing at all. Assigned
		// only when present, so a rollout without it leaves nil rather
		// than 0 — "instant" is not the same claim as "unmeasured".
		if e.DurationMS != nil {
			s.DurationMS = e.DurationMS
		}
		if e.TimeToFirstTokenMS != nil {
			s.TimeToFirstTokenMS = e.TimeToFirstTokenMS
		}
	}
}

type turnContext struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	ApprovalPolicy string `json:"approval_policy"`
}

// readTurnContext takes the model, reasoning effort and approval policy
// off a turn_context record.
//
// Last one wins. A session can change model or raise its approval policy
// part-way through, and the turn that finished the work is the one worth
// attributing — a session that started on a small model and escalated is
// better described by what it escalated to.
//
// Empty fields do not overwrite: not every turn_context carries every
// field, and a partial record must not erase what a complete one
// established.
func readTurnContext(s *journal.Session, payload json.RawMessage) {
	var tc turnContext
	if err := json.Unmarshal(payload, &tc); err != nil {
		return
	}
	if tc.Model != "" {
		s.Model = tc.Model
	}
	if tc.Effort != "" {
		s.Effort = tc.Effort
	}
	if tc.ApprovalPolicy != "" {
		// Codex calls it an approval policy and Claude Code calls it a
		// permission mode. Same question — how much the agent may do
		// unattended — so they share a column, with each agent's own
		// vocabulary preserved rather than mapped onto a common enum that
		// would have to guess at equivalences.
		s.PermissionMode = tc.ApprovalPolicy
	}
}

func int64p(v int64) *int64 { return &v }
