// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Token totals per model, and the cache-efficiency ratios
// derived from them.

package report

import (
	"sort"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// TokenUse is what one model cost, in tokens.
//
// Tokens, never currency. The obvious next step is multiplying by a price
// table, and it should not be taken: under a subscription the marginal
// token cost is zero, so a user on a fixed monthly plan spends the same
// whether a session burns 10k tokens or 10M. Multiplying their tokens by
// an API rate would report money nobody spent — not an imprecise figure, a
// categorically wrong one. Nothing in a transcript says which billing
// model a user is on.
//
// Anyone who needs money has their own contract and can multiply, and they
// will do it correctly for their own billing model, which this tool
// cannot.
type TokenUse struct {
	Model string

	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64

	// Reasoning is Codex-only; it stays zero for the others and
	// HasReasoning says which case a zero is.
	Reasoning    int64
	HasReasoning bool

	// Sessions is the denominator. A model with one enormous session and
	// one with fifty small ones can total the same, and only this
	// separates them.
	Sessions int
}

// Total is every input token the model was charged for, cached or not.
func (t TokenUse) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// CacheReadRatio is the share of input served from cache.
//
// The denominator deliberately includes cache WRITES as well as uncached
// input, because a write arrives uncached and is billed above base rate.
// Leaving it out is how an earlier analysis of this data reported a 99%
// hit rate where the real figure was 48% — the flattering error, and the
// one that would have got this panel dropped as showing nothing useful.
//
// Returns false when there is no input at all, rather than 0%.
func (t TokenUse) CacheReadRatio() (float64, bool) {
	denom := t.Input + t.CacheRead + t.CacheWrite
	if denom == 0 {
		return 0, false
	}
	return float64(t.CacheRead) / float64(denom), true
}

// WriteAmortisation is how many times each cached token was read back.
//
// A cache write costs 1.25x base and a read costs 0.1x, so a write needs
// roughly 1.25 reads before it pays for itself — see CacheBreakEven. Below
// that the caching cost more than it saved, which is the finding this
// number exists to surface and which a read ratio alone hides.
//
// Returns false when nothing was written to cache. That is the common case
// for Codex, which reports cache reads but never writes, and reporting an
// infinite or zero ratio there would be inventing a measurement (NAV-21).
func (t TokenUse) WriteAmortisation() (float64, bool) {
	if t.CacheWrite == 0 {
		return 0, false
	}
	return float64(t.CacheRead) / float64(t.CacheWrite), true
}

// CacheBreakEven is the amortisation below which a cache write lost money:
// the write premium (1.25x base) divided by the read discount (0.1x).
//
// Worth naming rather than inlining 1.25, because the obvious break-even
// is 1.0 and that is wrong — a series sitting at 1.10 still lost money
// while looking fine against a 1.0 line.
const CacheBreakEven = 1.25

// TokensByModel aggregates session token counts per model.
//
// Sessions reporting no tokens are skipped entirely rather than
// contributing zeroes. agy reports none at all, so including it would add
// a model row whose every figure is zero — which on a cost panel reads as
// "this agent is free" rather than "this agent does not say" (NAV-21).
//
// A session with tokens but no model is grouped under "" and rendered as
// unattributed rather than dropped: the tokens were really spent, and
// discarding them would understate the total.
func TokensByModel(sessions []journal.Session) []TokenUse {
	byModel := map[string]*TokenUse{}

	for _, s := range sessions {
		// The presence test is deliberately on any token field rather than
		// on the model: a session that reported usage counts even when the
		// model is unknown.
		if s.InputTokens == nil && s.OutputTokens == nil &&
			s.CacheReadTokens == nil && s.CacheWriteTokens == nil {
			continue
		}

		t, ok := byModel[s.Model]
		if !ok {
			t = &TokenUse{Model: s.Model}
			byModel[s.Model] = t
		}
		t.Sessions++
		t.Input += deref(s.InputTokens)
		t.Output += deref(s.OutputTokens)
		t.CacheRead += deref(s.CacheReadTokens)
		t.CacheWrite += deref(s.CacheWriteTokens)
		if s.ReasoningTokens != nil {
			t.Reasoning += *s.ReasoningTokens
			t.HasReasoning = true
		}
	}

	out := make([]TokenUse, 0, len(byModel))
	for _, t := range byModel {
		out = append(out, *t)
	}
	// Most expensive first: the model worth looking at is the one costing
	// the most, not the one whose name sorts first.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total() != out[j].Total() {
			return out[i].Total() > out[j].Total()
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// TokenTotals sums every model into one row, for the headline figure.
//
// The aggregate is reported alongside the per-model breakdown, never
// instead of it. A total hides exactly the finding that matters: one model
// sitting below cache break-even is invisible in a healthy-looking
// average.
func TokenTotals(rows []TokenUse) TokenUse {
	var all TokenUse
	all.Model = "all"
	for _, r := range rows {
		all.Input += r.Input
		all.Output += r.Output
		all.CacheRead += r.CacheRead
		all.CacheWrite += r.CacheWrite
		all.Reasoning += r.Reasoning
		all.HasReasoning = all.HasReasoning || r.HasReasoning
		all.Sessions += r.Sessions
	}
	return all
}

// BelowBreakEven returns the models whose cache writes did not pay for
// themselves — the actionable half of the cache numbers.
func BelowBreakEven(rows []TokenUse) []TokenUse {
	var out []TokenUse
	for _, r := range rows {
		if ratio, ok := r.WriteAmortisation(); ok && ratio < CacheBreakEven {
			out = append(out, r)
		}
	}
	return out
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
