package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

const fixture = "testdata/rollout-basic.jsonl"

// edits keeps only file-edit entries. ParseSince also emits a bare
// tool_call entry per non-editing call, which these tests are not about —
// counting both would make every edit assertion depend on how many other
// tools the fixture happens to contain.
func edits(entries []journal.Entry) []journal.Entry {
	var out []journal.Entry
	for _, e := range entries {
		if e.Event == "tool_use" {
			out = append(out, e)
		}
	}
	return out
}

func TestParseSinceReadsApplyPatch(t *testing.T) {
	all, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	entries := edits(all)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (one per apply_patch)", len(entries))
	}

	e := entries[0]
	if e.Agent != AgentName {
		t.Errorf("agent = %q, want %q", e.Agent, AgentName)
	}
	if e.AgentVersion != "0.147.0" {
		t.Errorf("version = %q, want it from session_meta", e.AgentVersion)
	}
	if e.Session != "sess-abc" {
		t.Errorf("session = %q, want it from session_meta", e.Session)
	}
	if e.Tool != "apply_patch" {
		t.Errorf("tool = %q", e.Tool)
	}
}

// A patch path is relative to the session's working directory. Recording it
// relative would make it unmatchable against a staged diff, which carries
// absolute paths.
func TestRelativePathsResolveAgainstSessionCwd(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := entries[0].File; got != "/repo/calc.py" {
		t.Fatalf("file = %q, want it joined to the session cwd", got)
	}
	// An already-absolute path must be left alone.
	if got := entries[1].File; got != "/abs/new.py" {
		t.Fatalf("absolute path was rewritten to %q", got)
	}
}

// NAV-54: a patch that did not apply is not agent-authored code. Counting
// its lines would attribute text that never reached the file.
func TestFailedPatchContributesNothing(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	var ok, failed = entries[0], entries[1]
	if ok.Outcome != "accepted" {
		t.Fatalf("first entry outcome = %q, want accepted", ok.Outcome)
	}
	if failed.Outcome != "failed" {
		t.Fatalf("second entry outcome = %q, want failed", failed.Outcome)
	}

	if ok.LinesAdded == 0 || len(ok.LineHashes) == 0 {
		t.Errorf("accepted patch recorded no lines: +%d hashes=%d", ok.LinesAdded, len(ok.LineHashes))
	}
	if failed.LinesAdded != 0 || failed.LinesRemoved != 0 || len(failed.LineHashes) != 0 {
		t.Errorf("failed patch contributed lines: +%d -%d hashes=%d",
			failed.LinesAdded, failed.LinesRemoved, len(failed.LineHashes))
	}
}

// Codex edits files through the shell far more often than through
// apply_patch. Those are ignored on purpose: the path is buried in an
// arbitrary shell string, and guessing produces confident wrong attribution
// (NAV-21).
func TestShellCommandsAreIgnored(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.File, "ignored.py") {
			t.Fatalf("a sed shell command was attributed: %+v", e)
		}
	}
}

func TestParseSinceFiltersByTime(t *testing.T) {
	cut := time.Date(2026, 8, 12, 10, 0, 4, 0, time.UTC)
	all, err := ParseSince(fixture, cut)
	if err != nil {
		t.Fatal(err)
	}
	entries := edits(all)
	if len(entries) != 1 {
		t.Fatalf("got %d entries after cutoff, want 1", len(entries))
	}
}

func TestParseSessionActivityCountsWithoutContent(t *testing.T) {
	acts, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("got %d sessions, want 1", len(acts))
	}
	a := acts[0]
	if a.UserMessages != 1 || a.AgentMessages != 1 {
		t.Errorf("messages: user=%d agent=%d, want 1/1", a.UserMessages, a.AgentMessages)
	}
	if a.ToolCalls != 3 {
		t.Errorf("tool calls = %d, want 3 (two patches, one shell)", a.ToolCalls)
	}
	if a.Session != "sess-abc" || a.Agent != AgentName {
		t.Errorf("session identity wrong: %+v", a)
	}
}

// The journal must never hold prompt text. Asserted on the parsed output
// rather than trusted, since the fixture contains a real prompt string.
func TestNoPromptTextIsExtracted(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		for _, field := range []string{e.File, e.Tool, e.Agent, e.Session, e.HunkHash} {
			if strings.Contains(field, "add a function") {
				t.Fatalf("prompt text leaked into a journal field: %q", field)
			}
		}
	}
}

func TestParsePatch(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: a.go\n@@\n-old\n+new\n+also new\n" +
		"*** Add File: b.go\n+created\n" +
		"*** Delete File: c.go\n" +
		"*** End Patch"

	files := parsePatch(patch)
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3", len(files))
	}
	if files[0].Path != "a.go" || files[0].Added != 2 || files[0].Removed != 1 {
		t.Errorf("update: %+v", files[0])
	}
	if files[1].Op != "Add" || files[1].Added != 1 {
		t.Errorf("add: %+v", files[1])
	}
	if files[2].Op != "Delete" || files[2].Added != 0 {
		t.Errorf("delete: %+v", files[2])
	}
}

// A truncated patch yields what it could read. Discarding the whole session
// because its last hunk was cut off loses evidence that is otherwise fine.
func TestParsePatchTolerantOfTruncation(t *testing.T) {
	files := parsePatch("*** Begin Patch\n*** Update File: a.go\n@@\n+one\n+two")
	if len(files) != 1 || files[0].Added != 2 {
		t.Fatalf("truncated patch: %+v", files)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		output string
		want   Outcome
	}{
		{"Success. Updated the following files:\nM /a.py\n", OutcomeAccepted},
		{"apply_patch verification failed: Failed to find expected lines", OutcomeFailed},
		{"The user rejected this tool call", OutcomeRejected},
		{"aborted by user", OutcomeRejected},
		{"", OutcomeUnknown},
	}
	for _, c := range cases {
		if got := classify(c.output); got != c.want {
			t.Errorf("classify(%.40q) = %q, want %q", c.output, got, c.want)
		}
	}
}

// Codex writes the output either plainly or wrapped in JSON, depending on
// version. Both must classify the same, or an upgrade silently turns every
// success into a failure.
func TestClassifyHandlesBothOutputShapes(t *testing.T) {
	plain := "Success. Updated the following files:\nM /a.py\n"
	wrapped := `{"output":"Success. Updated the following files:\nM /a.py\n","metadata":{"exit_code":0}}`

	if got := classify(unwrapOutput(plain)); got != OutcomeAccepted {
		t.Errorf("plain output classified %q", got)
	}
	if got := classify(unwrapOutput(wrapped)); got != OutcomeAccepted {
		t.Errorf("JSON-wrapped output classified %q", got)
	}
}

// SessionFiles matches by the cwd recorded inside each transcript, because
// Codex files sessions by date rather than by repository.
func TestSessionFilesMatchesByRecordedCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHODUNIT_CODEX_PATH", root)

	day := filepath.Join(root, "2026", "08", "12")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(t.TempDir(), "mine")
	theirs := filepath.Join(t.TempDir(), "theirs")
	for _, d := range []string{mine, theirs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(name, cwd string) {
		line := `{"timestamp":"2026-08-12T10:00:00.000Z","type":"session_meta","payload":{"id":"s","cwd":"` + cwd + `","cli_version":"1"}}` + "\n"
		if err := os.WriteFile(filepath.Join(day, name), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("rollout-mine.jsonl", mine)
	write("rollout-theirs.jsonl", theirs)

	got, err := SessionFiles(mine)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d files, want only this repository's: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "rollout-mine.jsonl") {
		t.Fatalf("matched the wrong session: %s", got[0])
	}
}

// Codex not installed is a fact, not an error — most machines have some
// agents and not others.
func TestSessionFilesOnMissingRootIsNotAnError(t *testing.T) {
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "nope"))
	files, err := SessionFiles("/repo")
	if err != nil {
		t.Fatalf("missing sessions root returned an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files from a missing root", len(files))
	}
}
