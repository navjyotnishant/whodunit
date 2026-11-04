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
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/compattest"
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
	compattest.Golden(t, "linehash.golden", b.String())
}
