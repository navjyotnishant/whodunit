// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The sections that make a claim, and the one that refuses to.

package report

import (
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// A metric that silently does not render is indistinguishable from one that
// was never built, and a zero in its place reads as a measurement.
func TestMissingMetricsNameThemselvesAndTheirFix(t *testing.T) {
	stats := Stats{TotalCommits: 10, Covered: 10, HasBaseline: false}
	act := Activity{Present: true, Outcomes: map[string]int{"accepted": 2}}

	items := unavailableFor(stats, act, stats.HasBaseline)
	if len(items) == 0 {
		t.Fatal("nothing reported as unavailable despite no baseline and no spend")
	}

	var haveBaseline, haveCost bool
	for _, it := range items {
		if it.Fix == "" {
			t.Errorf("%q says what is missing but not what would fix it", it.What)
		}
		if strings.Contains(it.What, "Before-and-after") {
			haveBaseline = true
		}
		if strings.Contains(it.What, "Cost") {
			haveCost = true
		}
	}
	if !haveBaseline || !haveCost {
		t.Errorf("expected both the baseline and cost gaps, got %d item(s)", len(items))
	}
}

// The inverse: a repository with everything configured must not be told it
// is missing things.
func TestNothingIsReportedMissingWhenEverythingExists(t *testing.T) {
	stats := Stats{TotalCommits: 10, Covered: 10, MonthlySpend: 200, HasBaseline: true}
	act := Activity{
		Present:  true,
		Outcomes: map[string]int{"accepted": 90, "rejected": 10},
	}

	if items := unavailableFor(stats, act, stats.HasBaseline); len(items) != 0 {
		t.Errorf("reported %d gap(s) on a fully configured repository: %+v", len(items), items)
	}
}

// Cost per thousand lines rather than per commit: commits vary with how
// people split them, which is not a property of the agent.
func TestCostIsPerThousandAgentWrittenLines(t *testing.T) {
	stats := Stats{MonthlySpend: 200, Assisted: 4}
	act := Activity{Present: true, LinesByTool: map[string]int{"Edit": 8000, "Write": 2000}}

	got, lines, ok := costPerThousandLines(stats, act)
	if !ok {
		t.Fatal("no cost computed despite spend and lines")
	}
	if lines != 10000 {
		t.Errorf("counted %d lines, want 10000", lines)
	}
	// $200 over 10,000 lines is $20 per thousand.
	if got < 19.99 || got > 20.01 {
		t.Errorf("cost = %.4f, want 20.00 per 1,000 lines", got)
	}
}

func TestCostIsWithheldWithoutSpend(t *testing.T) {
	act := Activity{Present: true, LinesByTool: map[string]int{"Edit": 8000}}
	if _, _, ok := costPerThousandLines(Stats{}, act); ok {
		t.Error("computed a cost with no spend configured")
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
