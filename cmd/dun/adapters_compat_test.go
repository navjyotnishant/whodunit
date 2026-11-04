package main

import (
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/adapter"
)

// The order agents are consulted in decides the order dun reports them and,
// when two agents touched the same lines, which session a trailer names first.
// Pinned before capture moves behind a public package (spec 002, task T001).
func TestCompatAdapterOrderAsDunSeesIt(t *testing.T) {
	var names []string
	for _, a := range adapter.All() {
		names = append(names, a.Name())
	}
	if got, want := strings.Join(names, ","), "agy,claude-code,codex"; got != want {
		t.Fatalf("adapter order = %s, want %s", got, want)
	}
}
