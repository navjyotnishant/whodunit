// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The sections that make a claim, and the one that refuses to.

package report

import (
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// A metric that silently does not render is indistinguishable from one that
// was never built, and a zero in its place reads as a measurement.
func TestMissingMetricsNameThemselvesAndTheirFix(t *testing.T) {
	stats := Stats{TotalCommits: 10, Covered: 10, HasBaseline: false}
	act := Activity{Present: true, Outcomes: map[string]int{"accepted": 2}}

	items := unavailableFor(stats, act, stats.HasBaseline)
	if len(items) == 0 {
		t.Fatal("nothing reported as unavailable despite no baseline and no tokens")
	}

	var haveBaseline, haveTokens bool
	for _, it := range items {
		if it.Fix == "" {
			t.Errorf("%q says what is missing but not what would fix it", it.What)
		}
		if strings.Contains(it.What, "Before-and-after") {
			haveBaseline = true
		}
		if strings.Contains(it.What, "Token") {
			haveTokens = true
		}
	}
	if !haveBaseline || !haveTokens {
		t.Errorf("expected both the baseline and token gaps, got %d item(s): %+v",
			len(items), items)
	}
}

// The inverse: a repository with everything configured must not be told it
// is missing things.
func TestNothingIsReportedMissingWhenEverythingExists(t *testing.T) {
	stats := Stats{TotalCommits: 10, Covered: 10, HasBaseline: true}
	act := Activity{
		Present:  true,
		Outcomes: map[string]int{"accepted": 90, "rejected": 10},
		Sessions: []journal.Session{{Model: "opus", InputTokens: int64Ptr(100)}},
	}

	if items := unavailableFor(stats, act, stats.HasBaseline); len(items) != 0 {
		t.Errorf("reported %d gap(s) on a fully configured repository: %+v", len(items), items)
	}
}

// monthly_spend was removed (NAV-96): it was a hand-typed number divided
// evenly over every line, which reported the same unit cost whether the
// agent ran once or a thousand times — an allocation dressed as a
// measurement. Measured tokens replace it.
//
// A repository whose sessions carry no token counts is told so, rather
// than shown zeros.
func TestTokenUseIsReportedMissingWhenNoSessionHasIt(t *testing.T) {
	act := Activity{
		Present: true,
		// The shape of an agy-only repository: sessions exist, none report
		// tokens.
		Sessions: []journal.Session{{Model: "gemini-3.7-flash-high"}},
		Outcomes: map[string]int{"accepted": 90, "rejected": 10},
	}

	var found bool
	for _, item := range unavailableFor(Stats{HasBaseline: true, TotalCommits: 10, Covered: 10}, act, true) {
		if strings.Contains(item.What, "Token") {
			found = true
		}
	}
	if !found {
		t.Error("a repository with no token data was not told so")
	}
}

// The survival split is the distinction the method names carry: "an agent
// worked on this" and "the agent's output is what shipped" are different
// claims, and only the second is evidence the work landed.
func TestSurvivalSeparatesIntersectedFromObserved(t *testing.T) {
	var w strings.Builder
	renderSurvival(&w, Stats{MethodCount: map[spec.Method]int{
		spec.MethodIntersected: 30,
		spec.MethodObserved:    10,
	}})
	out := w.String()

	if !strings.Contains(out, "survived to the commit") {
		t.Error("the intersected row is not labelled as surviving")
	}
	if !strings.Contains(out, "75%") {
		t.Errorf("expected 30 of 40 to render as 75%%:\n%s", out)
	}
}

// Undetermined commits carry no line-level evidence either way, so a
// repository with none of the two measured methods must render nothing
// rather than an empty chart implying zero survival.
func TestSurvivalRendersNothingWithoutEvidence(t *testing.T) {
	var w strings.Builder
	renderSurvival(&w, Stats{MethodCount: map[spec.Method]int{
		spec.MethodUndetermined: 50,
	}})
	if w.Len() != 0 {
		t.Errorf("rendered a survival section with no line-level evidence:\n%s", w.String())
	}
}

// Commit.Trailer is nil on every commit made before the hooks were
// installed, which in most repositories is nearly all of them. Rendering
// crashed on the first one.
func TestCommitSizeSurvivesCommitsWithoutTrailers(t *testing.T) {
	assisted := spec.Trailer{Status: spec.StatusAssisted}
	stats := Stats{Commits: []Commit{
		{Trailer: &assisted, LinesAdded: 100, LinesRemoved: 20},
		{Trailer: nil, LinesAdded: 10, LinesRemoved: 5},
		{Trailer: nil, LinesAdded: 30, LinesRemoved: 5},
	}}

	var w strings.Builder
	renderCommitSize(&w, stats) // must not panic

	out := w.String()
	if !strings.Contains(out, "AI-assisted") {
		t.Fatalf("no comparison rendered:\n%s", out)
	}
	// The two untrailered commits are the comparison group, averaging 25.
	if !strings.Contains(out, "25 ") {
		t.Errorf("untrailered commits were not counted as the comparison group:\n%s", out)
	}
}

func int64Ptr(v int64) *int64 { return &v }
