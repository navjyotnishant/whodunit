package spec

// Compatibility suite (ADR 0008 in whodunit-pro): trailers are written into
// commits and can never be rewritten, so their rendered form is frozen at v=2.
// Regenerate only for a deliberate, documented format change:
//
//	WHODUNIT_UPDATE_COMPAT=1 go test ./internal/spec -run Compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatGoldenTrailers(t *testing.T) {
	statuses := []Status{StatusAssisted, StatusUndetermined, StatusUnassisted, StatusUnmatched, StatusDegraded, StatusUninstrumented}
	methods := []Method{MethodUndetermined, MethodDeclared, MethodInferred, MethodObserved, MethodIntersected}

	var b strings.Builder
	fmt.Fprintf(&b, "Version=%d VersionKey=%s TrailerKey=%s\n", Version, VersionKey, TrailerKey)
	for _, s := range statuses {
		b.WriteString(WithStatus(s).Format() + "\n")
	}
	b.WriteString(Undetermined().Format() + "\n")

	ratio := 0.4567
	zero := 0.0
	for _, m := range methods {
		tr := Trailer{Status: StatusAssisted, Method: m, Agent: "claude-code", Version: "2.1.0", SpecVer: Version}
		b.WriteString(tr.Format() + "\n")
		tr.Ratio = &ratio
		tr.Model = "claude-opus-5"
		tr.Session = "sess-123"
		b.WriteString(tr.Format() + "\n")
	}
	for _, c := range []ChangedBy{ChangedByHuman, ChangedByAgent} {
		tr := Trailer{Status: StatusAssisted, Method: MethodObserved, Agent: "codex", Ratio: &zero, ChangedBy: c, SpecVer: Version}
		b.WriteString(tr.Format() + "\n")
	}
	b.WriteString(Trailer{Status: StatusAssisted, Method: MethodIntersected, Extra: map[string]string{"x_future": "1"}, SpecVer: Version}.Format() + "\n")

	// Every rendered trailer must still parse under today's reader.
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n")[1:] {
		if _, err := Parse(strings.TrimPrefix(line, TrailerKey+": ")); err != nil {
			t.Errorf("rendered trailer no longer parses: %q: %v", line, err)
		}
	}
	compatGolden(t, "trailers.golden", b.String())
}

func compatGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "compat", name)
	if os.Getenv("WHODUNIT_UPDATE_COMPAT") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (generate with WHODUNIT_UPDATE_COMPAT=1)", path, err)
	}
	if string(want) != got {
		t.Fatalf("compatibility break: output differs from %s.\n"+
			"Existing users' stored data depends on this output; see ADR 0008.\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
