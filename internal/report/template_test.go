package report

import (
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

func TestParseTemplate(t *testing.T) {
	if got, err := ParseTemplate(""); err != nil || got != TemplateExec {
		t.Errorf("empty name gave (%q, %v), want the exec default", got, err)
	}
	for _, name := range []string{"exec", "adoption", "detail"} {
		if _, err := ParseTemplate(name); err != nil {
			t.Errorf("ParseTemplate(%q): %v", name, err)
		}
	}
}

// A typo must not silently render a different report than the one asked
// for. The error names the alternatives so it is actionable.
func TestUnknownTemplateNamesTheValidOnes(t *testing.T) {
	_, err := ParseTemplate("adoptoin")
	if err == nil {
		t.Fatal("an unknown template was accepted")
	}
	for _, want := range []string{"exec", "adoption", "detail"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// NAV-73 criterion 7, and the one most worth guarding. Someone who has
// never used an agent, someone whose journal was purged, and someone whose
// agent path is misconfigured all produce the same empty journal — and a
// wall of zeros would tell all three "no AI was used here".
func TestEmptyJournalSaysSoRatherThanShowingZeros(t *testing.T) {
	stats := Stats{TotalCommits: 5, Covered: 5, Assisted: 3}

	for _, tmpl := range []Template{TemplateAdoption, TemplateDetail} {
		var b strings.Builder
		RenderTemplate(&b, stats, Activity{}, tmpl)
		out := b.String()

		if !strings.Contains(out, "No journal data") {
			t.Errorf("%s template does not say the journal is empty", tmpl)
		}
		if !strings.Contains(out, `not the same as "no AI was used"`) {
			t.Errorf("%s template does not distinguish absence from zero", tmpl)
		}
	}
}

// A rate without its denominator is a different claim: three decided calls
// and three hundred are not the same evidence (NAV-54).
func TestAcceptanceAlwaysCarriesItsDenominator(t *testing.T) {
	act := Activity{
		Present:  true,
		Events:   10,
		Outcomes: map[string]int{"accepted": 7, "rejected": 2, "failed": 1},
	}
	var b strings.Builder
	RenderTemplate(&b, Stats{TotalCommits: 1, Covered: 1}, act, TemplateExec)
	out := b.String()

	if !strings.Contains(out, "70%") {
		t.Errorf("acceptance rate missing:\n%s", out)
	}
	if !strings.Contains(out, "7 of 10 decided") {
		t.Errorf("acceptance rate has no denominator:\n%s", out)
	}
}

func TestAcceptanceRate(t *testing.T) {
	a := Activity{Outcomes: map[string]int{"accepted": 3, "rejected": 1}}
	rate, decided, ok := a.AcceptanceRate()
	if !ok || decided != 4 || rate != 0.75 {
		t.Fatalf("got (%v, %d, %v), want (0.75, 4, true)", rate, decided, ok)
	}

	// Unknown outcomes are not decided: counting them would flatter or
	// deflate the rate depending on which way it was rounded.
	b := Activity{Outcomes: map[string]int{"unknown": 5}}
	if _, _, ok := b.AcceptanceRate(); ok {
		t.Error("unknown-only outcomes reported a rate; nothing was decided")
	}
}

// The report must open with no network, so it works offline, behind a
// firewall, and as an email attachment.
func TestReportMakesNoExternalRequests(t *testing.T) {
	act := Activity{
		Present:  true,
		Events:   3,
		Outcomes: map[string]int{"accepted": 3},
		Tools:    map[string]int{"Edit": 3},
		Agents:   map[string]int{"claude-code": 3},
		Daily: []DayCount{
			{Day: time.Now().Add(-48 * time.Hour), Events: 1},
			{Day: time.Now().Add(-24 * time.Hour), Events: 2},
		},
	}
	stats := Stats{
		TotalCommits: 1, Covered: 1, Assisted: 1,
		MethodCount:  map[spec.Method]int{spec.MethodObserved: 1},
		PurposeCount: map[purpose.Purpose]int{purpose.Feature: 1},
	}

	for _, tmpl := range Templates {
		var b strings.Builder
		RenderTemplate(&b, stats, act, tmpl)
		out := b.String()

		for _, forbidden := range []string{"http://", "https://", "<script", "cdn."} {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s template contains %q; the report must be self-contained",
					tmpl, forbidden)
			}
		}
	}
}

// One day is not a trend. Drawing a line through a single point implies a
// shape the data does not have.
func TestSingleDayIsNotDrawnAsATrend(t *testing.T) {
	act := Activity{
		Present: true,
		Events:  3,
		Daily:   []DayCount{{Day: time.Now(), Events: 3}},
	}
	var b strings.Builder
	RenderTemplate(&b, Stats{TotalCommits: 1}, act, TemplateExec)
	out := b.String()

	if strings.Contains(out, "<polyline") {
		t.Error("a single day was drawn as a trend line")
	}
	if !strings.Contains(out, "Not enough history") {
		t.Error("a single day did not explain why there is no trend")
	}
}

func TestSparklineNeedsTwoPoints(t *testing.T) {
	if got := sparkline([]float64{1}, []string{"a"}, "#000"); got != "" {
		t.Errorf("one point produced a chart: %s", got)
	}
	got := sparkline([]float64{1, 5, 3}, []string{"a", "b", "c"}, "#000")
	if !strings.Contains(got, "<polyline") {
		t.Errorf("three points produced no line: %s", got)
	}
}

// A flat series of zeros must render rather than divide by zero.
func TestSparklineHandlesAllZeroes(t *testing.T) {
	got := sparkline([]float64{0, 0, 0}, []string{"a", "b", "c"}, "#000")
	if !strings.Contains(got, "<polyline") {
		t.Errorf("an all-zero series produced no chart: %s", got)
	}
	if strings.Contains(got, "NaN") || strings.Contains(got, "+Inf") {
		t.Errorf("an all-zero series produced a degenerate chart: %s", got)
	}
}

func TestActivitySessionsRender(t *testing.T) {
	act := Activity{
		Present: true,
		Events:  1,
		Sessions: []journal.Session{{
			Session: "abcdef1234567890", Agent: "claude-code",
			UserMessages: 12, ToolCalls: 40, DistinctTools: 6,
		}},
	}
	var b strings.Builder
	RenderTemplate(&b, Stats{TotalCommits: 1}, act, TemplateAdoption)
	out := b.String()

	if !strings.Contains(out, "abcdef12") {
		t.Errorf("session id missing:\n%s", out)
	}
	if !strings.Contains(out, "no message content") {
		t.Error("the sessions table does not say it holds counts only")
	}
}

func TestTopToolsOrdersByCount(t *testing.T) {
	a := Activity{Tools: map[string]int{"Edit": 5, "Write": 12, "Bash": 1}}
	got := a.TopTools(2)
	if len(got) != 2 || got[0] != "Write" || got[1] != "Edit" {
		t.Fatalf("TopTools = %v, want [Write Edit]", got)
	}
}
