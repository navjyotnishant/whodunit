package linehash

// Compatibility suite (ADR 0008 in whodunit-pro): the line hash is a stored
// format. Hashes written by one release are read back by the next, from the
// local journal and from DevLake, so any change to Of's output silently stops
// matching work recorded before an upgrade. These goldens pin today's output.
//
// Regenerate only for a deliberate, documented format change:
//
//	WHODUNIT_UPDATE_COMPAT=1 go test ./internal/linehash -run Compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var compatPaths = []string{
	"/repo/main.go",
	"/repo//main.go",
	"/repo/./pkg/../main.go",
	`C:\repo\main.go`,
	"C:/repo/main.go",
	"/private/tmp/repo/a b/ünïcode.go",
	"relative/path.go",
	"",
}

var compatLines = []string{
	"package main",
	"\tfmt.Println(\"hello\")   ",
	"}",
	"",
	"    ",
	"// ünïcode comment ✓",
	"return nil",
}

func TestCompatGoldenLineHashes(t *testing.T) {
	var b strings.Builder
	for _, p := range compatPaths {
		for _, l := range compatLines {
			fmt.Fprintf(&b, "%q\t%q\t%016x\n", p, l, Of(p, l))
		}
	}
	text := "line one\n\tline two\n}\n\nlast line without newline"
	fmt.Fprintf(&b, "OfText %q:", text)
	for _, h := range OfText("/repo/main.go", text) {
		fmt.Fprintf(&b, " %016x", h)
	}
	b.WriteString("\n")
	for _, l := range compatLines {
		fmt.Fprintf(&b, "Substantive %q = %v\n", l, Substantive(l))
	}
	compatGolden(t, "linehash.golden", b.String())
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
