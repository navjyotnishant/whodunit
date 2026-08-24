package report

import (
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Every method needs its own colour, including the two nothing emitted
// until Copilot support landed. A method falling through to the default
// would render `declared` in the same grey as `undetermined`, equating an
// agent's own claim with no evidence at all.
func TestEveryMethodHasADistinctColour(t *testing.T) {
	seen := map[string]spec.Method{}
	for _, m := range []spec.Method{
		spec.MethodIntersected, spec.MethodObserved, spec.MethodInferred,
		spec.MethodDeclared, spec.MethodUndetermined,
	} {
		c := methodColor(m)
		if c == "" {
			t.Errorf("%s has no colour", m)
		}
		if prev, dup := seen[c]; dup {
			t.Errorf("%s and %s share colour %s", m, prev, c)
		}
		seen[c] = m
	}
}

// undetermined is an honest unknown, not a failure. Rendering it red would
// read as an error state and invite someone to "fix" commits that simply
// had no evidence either way (NAV-21).
func TestUndeterminedIsNotRed(t *testing.T) {
	c := methodColor(spec.MethodUndetermined)
	if strings.Contains(strings.ToLower(c), "ef4444") || strings.HasPrefix(c, "#f") {
		t.Errorf("undetermined rendered as %s, which reads as a failure", c)
	}
}
