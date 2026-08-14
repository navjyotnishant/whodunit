package spec

import (
	"strings"
	"testing"
)

func ratioPtr(f float64) *float64 { return &f }

func TestFormatParseRoundTrip(t *testing.T) {
	orig := Trailer{
		Status:  StatusAssisted,
		Method:  MethodIntersected,
		Agent:   "claude-code",
		Version: "2.1.0",
		Ratio:   ratioPtr(0.62),
		Session: "a3f9e21c",
		Extra:   map[string]string{},
	}
	formatted := orig.Format()

	value := formatted[len(TrailerKey)+2:] // strip "AI-Attribution: "
	got, err := Parse(value)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Status != orig.Status || got.Method != orig.Method || got.Agent != orig.Agent {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, orig)
	}
	if got.Ratio == nil {
		t.Fatal("ratio lost in round trip")
	}
	if *got.Ratio != *orig.Ratio {
		t.Errorf("ratio = %v, want %v", *got.Ratio, *orig.Ratio)
	}
}

func TestFormatOmitsUncomputedRatio(t *testing.T) {
	// An unknown ratio must be absent, not 0.00 — a fabricated zero reads
	// as "the agent contributed nothing" (NAV-8).
	tr := Trailer{
		Status: StatusAssisted,
		Method: MethodObserved,
		Agent:  "claude-code",
		Extra:  map[string]string{},
	}
	if got := tr.Format(); strings.Contains(got, "ratio=") {
		t.Errorf("Format() = %q, want no ratio when it was not computed", got)
	}
}

func TestParseOmittedRatioIsNil(t *testing.T) {
	got, err := Parse("status=assisted; method=observed; agent=claude-code")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Ratio != nil {
		t.Errorf("Ratio = %v, want nil when the trailer carries no ratio", *got.Ratio)
	}
}

func TestUndeterminedNeverNone(t *testing.T) {
	tr := Undetermined()
	if tr.Status != StatusUndetermined || tr.Method != MethodUndetermined {
		t.Errorf("Undetermined() = %+v, want status/method both undetermined", tr)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := []string{
		"status=bogus; method=declared",
		"status=assisted; method=bogus",
		"status=assisted",                             // missing method
		"status=assisted; method=declared; ratio=1.5", // out of range
		"status=assisted; method=declared; agent=has spaces",
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", c)
		}
	}
}

func TestParsePreservesUnknownKeys(t *testing.T) {
	got, err := Parse("status=assisted; method=declared; future_key=xyz")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Extra["future_key"] != "xyz" {
		t.Errorf("unknown key not preserved: %+v", got.Extra)
	}
}
