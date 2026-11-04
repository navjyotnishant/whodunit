// Package compattest holds the golden-file check behind the compatibility
// suite (ADR 0008 in whodunit-pro). Each package's compat_test.go renders
// a stored format existing users depend on and compares it with a golden in
// that package's testdata/compat directory.
//
// Regenerate goldens only for a deliberate, documented format change:
//
//	WHODUNIT_UPDATE_COMPAT=1 make compat
package compattest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UpdateEnv is the environment variable that rewrites goldens instead of
// comparing against them.
const UpdateEnv = "WHODUNIT_UPDATE_COMPAT"

// Golden compares got with testdata/compat/<name> in the calling package.
func Golden(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "compat", name)
	if os.Getenv(UpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (generate with %s=1)", path, err, UpdateEnv)
	}
	// A Windows checkout with autocrlf turns the golden's line endings into
	// CRLF. That is a property of the checkout, not of the format under test.
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if want != got {
		t.Fatalf("compatibility break: output differs from %s.\n"+
			"Existing users' stored data depends on this output; see ADR 0008.\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
