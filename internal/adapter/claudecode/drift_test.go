package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Golden-fixture replay (NAV-30).
//
// The spec says an unrecognized transcript format fails to `undetermined`
// rather than guessing. Nothing detects that it happened: if Claude Code
// changes its format, ParseSince quietly returns fewer events, attribution
// degrades, and every downstream number (status, report, delta) inherits
// the rot with no signal.
//
// testdata/ holds one fixture per Claude Code version seen in the wild —
// real record structure, synthetic paths and content. These tests fail
// loudly when a format this adapter used to understand stops parsing.

func fixturePaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "claude-code-*.jsonl"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures in testdata/ — drift detection cannot work without them")
	}
	return paths
}

func TestEveryFixtureVersionStillParses(t *testing.T) {
	for _, path := range fixturePaths(t) {
		version := versionFromFixtureName(path)
		t.Run(version, func(t *testing.T) {
			entries, err := ParseSince(path, time.Time{})
			if err != nil {
				t.Fatalf("ParseSince: %v", err)
			}

			// Every fixture contains at least one Edit/Write tool_use.
			// Zero entries means this version's format is no longer
			// understood — the exact silent failure this test exists for.
			if len(entries) == 0 {
				t.Fatalf("fixture for Claude Code %s parsed to zero entries: "+
					"the adapter no longer understands a format it used to. "+
					"Attribution for this version now silently degrades to undetermined.", version)
			}

			for _, e := range entries {
				if e.Agent != AgentName {
					t.Errorf("Agent = %q, want %q", e.Agent, AgentName)
				}
				if e.AgentVersion != version {
					t.Errorf("AgentVersion = %q, want %q", e.AgentVersion, version)
				}
				if e.File == "" {
					t.Error("entry has no file path")
				}
				if e.HunkHash == "" {
					t.Error("entry has no hunk hash, so it can never reach method=intersected")
				}
				if e.Timestamp.IsZero() {
					t.Error("entry has no timestamp")
				}
				if e.Tool != "Edit" && e.Tool != "Write" {
					t.Errorf("Tool = %q, want Edit or Write", e.Tool)
				}
			}
		})
	}
}

func TestNoFixtureLeaksPromptText(t *testing.T) {
	// Each fixture's user turn contains a marker string. The journal has no
	// field that could hold it, but this asserts the guarantee end to end
	// rather than trusting the struct definition alone.
	const marker = "PROMPT TEXT MUST NEVER BE PARSED"

	for _, path := range fixturePaths(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), marker) {
			t.Fatalf("%s is missing the prompt-text marker, so this test proves nothing", path)
		}

		entries, err := ParseSince(path, time.Time{})
		if err != nil {
			t.Fatalf("ParseSince %s: %v", path, err)
		}
		for _, e := range entries {
			for field, value := range map[string]string{
				"File": e.File, "Tool": e.Tool, "Agent": e.Agent,
				"Session": e.Session, "HunkHash": e.HunkHash, "Event": e.Event,
			} {
				if strings.Contains(value, marker) {
					t.Errorf("%s: prompt text reached journal field %s", path, field)
				}
			}
		}
	}
}

func TestFixtureCoversBothEditAndWrite(t *testing.T) {
	// A fixture set that only exercises one tool would miss a format change
	// affecting the other.
	sawEdit, sawWrite := false, false
	for _, path := range fixturePaths(t) {
		entries, err := ParseSince(path, time.Time{})
		if err != nil {
			t.Fatalf("ParseSince %s: %v", path, err)
		}
		for _, e := range entries {
			switch e.Tool {
			case "Edit":
				sawEdit = true
			case "Write":
				sawWrite = true
			}
		}
	}
	if !sawEdit || !sawWrite {
		t.Errorf("fixture set covers Edit=%v Write=%v; both must be represented", sawEdit, sawWrite)
	}
}

func TestUnrecognizedFormatYieldsNothingRatherThanGuessing(t *testing.T) {
	// A future format the adapter doesn't understand must produce zero
	// entries, never a partially-guessed one.
	dir := t.TempDir()
	path := filepath.Join(dir, "future.jsonl")
	future := `{"schemaVersion":9,"kind":"assistant","events":[{"kind":"fileEdit","target":"/repo/x.go"}]}` + "\n"
	if err := os.WriteFile(path, []byte(future), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := ParseSince(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince on an unknown format = %v; it should degrade, not error", err)
	}
	if len(entries) != 0 {
		t.Errorf("unknown format produced %d entries; it must produce none rather than guess", len(entries))
	}
}

func versionFromFixtureName(path string) string {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "claude-code-")
	return strings.TrimSuffix(base, ".jsonl")
}
