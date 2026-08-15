package report

import (
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

func tok(v int64) *int64 { return &v }

// The cache read ratio counts cache WRITES as uncached, because a write
// arrives uncached and is billed above base rate.
//
// Leaving writes out of the denominator is how an earlier analysis of this
// same data reported a 99% hit rate where the real figure was 48% — the
// flattering error, and the one that would have got the whole panel
// dropped as showing nothing actionable.
func TestCacheReadRatioCountsWritesAsUncached(t *testing.T) {
	u := TokenUse{Input: 100, CacheRead: 600, CacheWrite: 300}

	got, ok := u.CacheReadRatio()
	if !ok {
		t.Fatal("no ratio computed")
	}
	// 600 / (100 + 600 + 300) = 60%. Excluding writes would give
	// 600/700 = 86%.
	if got < 0.599 || got > 0.601 {
		t.Errorf("ratio = %.3f, want 0.600 — cache writes are missing from the "+
			"denominator, which flatters the figure", got)
	}
}

// A write costs about 1.25x base and a read 0.1x, so a write needs roughly
// 1.25 reads before it pays for itself. The obvious break-even is 1.0 and
// that is wrong: a series at 1.10 still lost money.
func TestBelowBreakEvenFlagsALossThatLooksFine(t *testing.T) {
	rows := []TokenUse{
		{Model: "profitable", CacheRead: 5000, CacheWrite: 1000}, // 5.00x
		{Model: "looks-fine", CacheRead: 1100, CacheWrite: 1000}, // 1.10x — a loss
		{Model: "clear-loss", CacheRead: 730, CacheWrite: 1000},  // 0.73x
	}

	below := BelowBreakEven(rows)
	if len(below) != 2 {
		t.Fatalf("flagged %d model(s), want 2 — a ratio above 1.0 but below %.2f "+
			"is still a loss", len(below), CacheBreakEven)
	}

	var names []string
	for _, r := range below {
		names = append(names, r.Model)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "looks-fine") {
		t.Errorf("flagged %s; the 1.10x model was treated as healthy", joined)
	}
	if strings.Contains(joined, "profitable") {
		t.Errorf("flagged %s; a 5x model is not a loss", joined)
	}
}

// Codex reports cache reads but never writes, so amortisation is
// unanswerable there. It must say so rather than return a number.
//
// Returning 0 would put Codex at the bottom of every cache-efficiency
// ranking on the strength of a measurement that was never taken; returning
// infinity would put it at the top. Both are inventions (NAV-21).
func TestAmortisationIsUnansweredWithoutCacheWrites(t *testing.T) {
	u := TokenUse{Input: 1000, CacheRead: 50000} // no writes: the Codex shape

	if v, ok := u.WriteAmortisation(); ok {
		t.Errorf("amortisation = %.2f for an agent that never reports cache "+
			"writes; the denominator was never measured", v)
	}
}

// A session reporting no tokens at all contributes nothing — it does not
// become a model row of zeroes.
//
// This is the agy shape, and a zero row on a cost panel reads as "this
// agent is free" rather than "this agent does not say".
func TestSessionsWithoutTokensDoNotBecomeZeroRows(t *testing.T) {
	rows := TokensByModel([]journal.Session{
		{Model: "claude-opus-5", InputTokens: tok(100), OutputTokens: tok(20)},
		{Model: "gemini-3.7-flash-high"}, // agy: reports nothing
		{Model: "gemini-3.7-flash-high"},
	})

	if len(rows) != 1 {
		t.Fatalf("got %d model rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Model != "claude-opus-5" {
		t.Errorf("row is %q; an agent reporting nothing became a row of zeroes",
			rows[0].Model)
	}
}

// Tokens with no model are kept and shown as unattributed rather than
// dropped. They were really spent, and discarding them understates the
// total — a different error from the one above, in the opposite direction.
func TestTokensWithNoModelAreKeptAsUnattributed(t *testing.T) {
	rows := TokensByModel([]journal.Session{
		{Model: "", InputTokens: tok(500)},
		{Model: "claude-opus-5", InputTokens: tok(100)},
	})

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 — unattributed tokens were dropped", len(rows))
	}
	total := TokenTotals(rows)
	if total.Input != 600 {
		t.Errorf("total input = %d, want 600", total.Input)
	}
}

// Rows are ordered most expensive first: the model worth looking at is the
// one costing the most, not the one whose name sorts first.
func TestRowsAreOrderedByTotalCost(t *testing.T) {
	rows := TokensByModel([]journal.Session{
		{Model: "aardvark", InputTokens: tok(10)},
		{Model: "zebra", InputTokens: tok(9000)},
	})

	if rows[0].Model != "zebra" {
		t.Errorf("first row is %q, want zebra — rows are sorted by name rather "+
			"than by cost", rows[0].Model)
	}
}

// Reasoning tokens are Codex-only. HasReasoning distinguishes "zero
// reasoning tokens" from "this agent does not separate them".
func TestReasoningTokensAreDistinguishedFromUnreported(t *testing.T) {
	claudeOnly := TokensByModel([]journal.Session{
		{Model: "claude-opus-5", InputTokens: tok(100)},
	})
	if claudeOnly[0].HasReasoning {
		t.Error("HasReasoning is true for Claude Code, which does not separate them")
	}

	codex := TokensByModel([]journal.Session{
		{Model: "gpt-5.5", InputTokens: tok(100), ReasoningTokens: tok(0)},
	})
	if !codex[0].HasReasoning {
		t.Error("HasReasoning is false for a Codex session that reported zero — " +
			"zero reasoning and no reasoning split are different answers")
	}
}

// The aggregate must not be the only thing shown. A model below break-even
// is invisible in a healthy-looking total, which is the entire reason the
// panel is per model.
func TestTheAggregateCanHideALoss(t *testing.T) {
	rows := TokensByModel([]journal.Session{
		{Model: "big", InputTokens: tok(1000), CacheReadTokens: tok(900_000), CacheWriteTokens: tok(100_000)},
		{Model: "small", InputTokens: tok(40), CacheReadTokens: tok(730), CacheWriteTokens: tok(1000)},
	})

	all := TokenTotals(rows)
	aggregate, _ := all.WriteAmortisation()
	if aggregate < CacheBreakEven {
		t.Fatal("the aggregate itself is below break-even; the fixture does not " +
			"demonstrate the hiding")
	}

	if len(BelowBreakEven(rows)) != 1 {
		t.Errorf("the per-model view did not surface the loss the aggregate "+
			"(%.2fx) hides", aggregate)
	}
}

// The report must not render a currency figure anywhere. Under a
// subscription the marginal token cost is zero, so a price table reports
// money nobody spent — and nothing in a transcript says which billing
// model a user is on.
func TestTheTokenPanelShowsNoCurrency(t *testing.T) {
	var w strings.Builder
	renderTokensByModel(&w, Activity{
		Present: true,
		Sessions: []journal.Session{
			{Model: "claude-opus-5", InputTokens: tok(1000), OutputTokens: tok(200),
				CacheReadTokens: tok(50000), CacheWriteTokens: tok(10000)},
		},
	})

	out := w.String()
	if strings.Contains(out, "$") {
		t.Errorf("the token panel renders a currency symbol:\n%s", out)
	}
	// And it says why, so nobody adds one back.
	if !strings.Contains(out, "not currency") {
		t.Error("the panel does not explain why it reports tokens rather than money")
	}
}
