package spec

import "testing"

func TestFormatParseRoundTrip(t *testing.T) {
	orig := Trailer{
		Status:  StatusAssisted,
		Method:  MethodIntersected,
		Agent:   "claude-code",
		Version: "2.1.0",
		Ratio:   0.62,
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
		"status=assisted",                    // missing method
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
