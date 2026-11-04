package linehash

import (
	"bytes"
	"testing"

	internal "github.com/navjyotnishant/whodunit/internal/linehash"
)

// The public package must hash exactly as dun does; it adds no logic.
func TestPublicMatchesInternal(t *testing.T) {
	k, err := NewKey(1, bytes.Repeat([]byte{3}, MinKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"package main", "\treturn nil  ", "}"} {
		if Of("/repo/main.go", l) != internal.Of("/repo/main.go", l) {
			t.Errorf("Of differs for %q", l)
		}
		if OfKeyed(k, "main.go", l) != internal.OfKeyed(k, "main.go", l) {
			t.Errorf("OfKeyed differs for %q", l)
		}
		if Substantive(l) != internal.Substantive(l) {
			t.Errorf("Substantive differs for %q", l)
		}
	}
	text := "a := 1\n}\nreturn nil"
	if len(OfText("/r/m.go", text)) != len(OfTextKeyed(k, "m.go", text)) {
		t.Error("OfText and OfTextKeyed keep different lines")
	}
}
