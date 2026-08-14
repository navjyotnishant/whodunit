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

// Every trailer carries a format version (NAV-118).
//
// It exists because a trailer's values have meanings that can change while
// staying parseable. `ratio=0.66` is well-formed under any definition of
// ratio, so without a version a reader has no way to know which rules
// produced it — and trailers are in commit messages, so the ambiguity is
// permanent the moment it appears.
func TestEveryTrailerCarriesAVersion(t *testing.T) {
	for name, tr := range map[string]Trailer{
		"assisted":     {Status: StatusAssisted, Method: MethodIntersected},
		"undetermined": Undetermined(),
	} {
		out := tr.Format()
		if !strings.Contains(out, VersionKey+"=1") {
			t.Errorf("%s trailer carries no version: %s", name, out)
		}
	}
}

// The version is emitted first, before the values it qualifies. A version
// discovered after the number it describes has already been read arrived
// too late to be useful.
func TestTheVersionComesFirst(t *testing.T) {
	out := Trailer{Status: StatusAssisted, Method: MethodObserved}.Format()
	want := TrailerKey + ": " + VersionKey + "=1;"
	if !strings.HasPrefix(out, want) {
		t.Errorf("trailer starts %q, want the version first: %s",
			out[:min(len(out), 40)], out)
	}
}

// An absent version is version 1, permanently.
//
// Every trailer written before the key existed is implicitly the first
// format, and nothing can be added to those commits — so this is the rule
// rather than a migration step to be removed later.
func TestAnAbsentVersionIsVersionOne(t *testing.T) {
	tr, err := Parse("status=assisted; method=observed; agent=claude-code")
	if err != nil {
		t.Fatalf("a trailer written before versioning failed to parse: %v", err)
	}
	if got := tr.SpecVersion(); got != 1 {
		t.Errorf("SpecVersion() = %d for an unversioned trailer, want 1", got)
	}
}

// A version that is not a positive integer is malformed rather than
// ignored. Silently accepting it would let a typo produce a trailer that
// claims a version it does not have.
func TestAnInvalidVersionIsRejected(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		if _, err := Parse("v=" + bad + "; status=assisted; method=observed"); err == nil {
			t.Errorf("v=%s was accepted", bad)
		}
	}
}

// A trailer written by a newer version must survive an older parser.
// Unknown keys go to Extra and are re-emitted, so a round trip through a
// version that does not understand `model=` does not silently drop it.
func TestUnknownKeysSurviveARoundTrip(t *testing.T) {
	in := "v=2; status=assisted; method=intersected; model=claude-opus-5"
	tr, err := Parse(in)
	if err != nil {
		t.Fatalf("a newer trailer failed to parse: %v", err)
	}
	if tr.SpecVersion() != 2 {
		t.Errorf("SpecVersion() = %d, want 2", tr.SpecVersion())
	}
	if tr.Extra["model"] != "claude-opus-5" {
		t.Errorf("unknown key dropped: %v", tr.Extra)
	}
	if !strings.Contains(tr.Format(), "model=claude-opus-5") {
		t.Errorf("unknown key not re-emitted: %s", tr.Format())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
