package termcolor

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A buffer is not a terminal, so the common case in tests and in every
// piped invocation must come out clean.
func TestBufferGetsNoColor(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	if w.Enabled() {
		t.Fatal("color enabled for a non-terminal writer")
	}
	if got := w.S(Intersected, "x"); got != "x" {
		t.Fatalf("got %q, want unstyled %q", got, "x")
	}
}

// Criterion 4: NO_COLOR wins even where color would otherwise apply.
func TestNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	if shouldColor(os.Stdout) {
		t.Fatal("NO_COLOR set but color still enabled")
	}
}

// When both are set the refusal wins. Missed on the first pass because
// the NO_COLOR test cleared FORCE_COLOR, so the conflict was never
// exercised — a real terminal showed color with NO_COLOR=1 set.
func TestNoColorBeatsForceColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "1")
	if shouldColor(os.Stdout) {
		t.Fatal("FORCE_COLOR overrode NO_COLOR; the opt-out must win")
	}
}

// CI logs are read after the fact, out of any terminal.
func TestCIGetsNoColor(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("FORCE_COLOR", "")
	if shouldColor(os.Stdout) {
		t.Fatal("CI set but color still enabled")
	}
}

func TestDumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CI", "")
	if shouldColor(os.Stdout) {
		t.Fatal("TERM=dumb but color still enabled")
	}
}

// Criterion 5/6: the hooks and the CI gate opt out structurally, so a
// terminal-attached run still writes plain text.
func TestPlainNeverColors(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	w := Plain(os.Stdout)
	if w.Enabled() {
		t.Fatal("Plain writer reported color enabled")
	}
	if got := w.S(Bold, "commit message"); got != "commit message" {
		t.Fatalf("Plain writer styled output: %q", got)
	}
}

// A styled string must reset, or its color bleeds into later output.
func TestStyleResets(t *testing.T) {
	w := &Writer{Writer: &bytes.Buffer{}, enabled: true}
	got := w.S(Observed, "obs")
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("style not reset: %q", got)
	}
	if !strings.HasPrefix(got, string(Observed)) {
		t.Fatalf("style not applied: %q", got)
	}
}

// NAV-21: undetermined is an honest unknown. Red would read as a failure
// and assert the very thing the spec refuses to assert.
func TestUndeterminedIsNotRed(t *testing.T) {
	s := MethodStyle("undetermined")
	if s != Undetermined {
		t.Fatalf("undetermined mapped to %q", s)
	}
	for _, red := range []string{"31m", "38;5;9m", "38;5;1m", "38;5;196m"} {
		if strings.Contains(string(s), red) {
			t.Fatalf("undetermined uses a red code (%s): %q", red, s)
		}
	}
}

// An unrecognized method must still read as "no evidence" rather than as
// unstyled prose, so a future method name cannot silently look confident.
func TestUnknownMethodFallsBackToUndetermined(t *testing.T) {
	if got := MethodStyle("something-new"); got != Undetermined {
		t.Fatalf("unknown method got %q, want undetermined", got)
	}
}

func TestEveryMethodHasADistinctStyle(t *testing.T) {
	seen := map[Style]string{}
	for _, m := range []string{"intersected", "observed", "inferred", "declared"} {
		s := MethodStyle(m)
		if prev, dup := seen[s]; dup {
			t.Fatalf("%s and %s share style %q", prev, m, s)
		}
		seen[s] = m
	}
}

// A nil Writer is what a zero-value struct field yields; it must not panic.
func TestNilWriterIsSafe(t *testing.T) {
	var w *Writer
	if w.Enabled() {
		t.Fatal("nil writer reported enabled")
	}
	if got := w.S(Bold, "x"); got != "x" {
		t.Fatalf("nil writer styled output: %q", got)
	}
}
