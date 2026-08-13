// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The log stays bounded, and rotation loses only the oldest.

package hooklog

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// fill writes until the log has rotated n times.
func fill(t *testing.T, home string, rotations int) {
	t.Helper()
	// Each entry is padded so a megabyte is reached in a few thousand
	// writes rather than a few million.
	pad := strings.Repeat("x", 512)
	for r := 0; r <= rotations; r++ {
		for i := 0; i < 2600; i++ {
			Write(home, Entry{Hook: "prepare-commit-msg", Event: "determine",
				Detail: fmt.Sprintf("gen%d-%d %s", r, i, pad)})
		}
	}
}

// Rotation must shift generations along rather than overwrite: losing a
// middle generation would leave a hole in the history that nothing reports.
func TestRotationKeepsEveryGeneration(t *testing.T) {
	home := t.TempDir()
	fill(t, home, 2)

	for i := 1; i <= 2; i++ {
		p := genPath(home, i)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("generation %d is missing after two rotations: %v", i, err)
		}
	}
}

// Read has to span every generation. A reader asking "when did this start
// failing" gets a wrong answer if only the live file is searched.
func TestReadSpansGenerations(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{Hook: "pre-push", Event: "sync", Detail: "the oldest entry"})
	fill(t, home, 1)
	Write(home, Entry{Hook: "pre-push", Event: "sync", Detail: "the newest entry"})

	entries, err := Read(home, 0)
	if err != nil {
		t.Fatal(err)
	}

	var sawOldest, sawNewest bool
	for _, e := range entries {
		switch e.Detail {
		case "the oldest entry":
			sawOldest = true
		case "the newest entry":
			sawNewest = true
		}
	}
	if !sawOldest {
		t.Error("an entry from a rotated generation was not returned")
	}
	if !sawNewest {
		t.Error("the most recent entry was not returned")
	}
	if entries[0].Detail != "the newest entry" {
		t.Errorf("newest entry is not first: got %q", entries[0].Detail)
	}
}
