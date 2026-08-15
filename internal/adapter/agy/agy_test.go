package agy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixture = "testdata/conversation.db"

func TestParseSinceReadsBothEditTools(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Three distinct edits: one replace, two writes. Read-only tool calls
	// contribute nothing.
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}

	byTool := map[string]int{}
	for _, e := range entries {
		byTool[e.Tool]++
		if e.Agent != AgentName {
			t.Errorf("agent = %q", e.Agent)
		}
	}
	if byTool["replace_file_content"] != 1 || byTool["write_file"] != 2 {
		t.Fatalf("tool mix wrong: %v", byTool)
	}
}

// A call and its result carry identical arguments, so the same edit appears
// twice in the steps table. Counting both would double every line an agent
// wrote.
func TestCallAndResultAreNotCountedTwice(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range entries {
		seen[e.File+e.Tool]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Fatalf("%s recorded %d times; call and result were both counted", k, n)
		}
	}
}

// The produced text is what earns `intersected`: it has to be hashed, and
// the counts have to reflect it.
func TestProducedTextIsHashed(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.LineHashes) == 0 {
			t.Errorf("%s produced no line hashes", e.File)
		}
		if e.LinesAdded == 0 {
			t.Errorf("%s recorded no added lines", e.File)
		}
		if e.HunkHash == "" {
			t.Errorf("%s has no hunk hash", e.File)
		}
	}
}

// A replace records what it removed as well as what it added; a fresh
// write removes nothing.
func TestLineCounts(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch {
		case e.Tool == "replace_file_content" && e.LinesRemoved == 0:
			t.Errorf("replace on %s removed nothing", e.File)
		case e.Tool == "write_file" && e.LinesRemoved != 0:
			t.Errorf("fresh write on %s removed %d lines", e.File, e.LinesRemoved)
		}
	}
}

// agy DOES carry an accept/reject signal, in steps.status.
//
// This test replaces one that asserted the opposite. That test passed
// because the fixture carried status 0 on every row — a value that does
// not occur in real databases — so it proved only that an unrecognised
// status maps to "unknown". The fixture now carries the statuses actually
// observed in the wild, and each maps to a distinct outcome.
func TestOutcomeComesFromStepStatus(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, e := range entries {
		got[e.File] = e.Outcome
	}

	want := map[string]string{
		"/repo/calc.py":       "accepted", // status 3
		"/repo/util.py":       "rejected", // status 7, "Permission denied"
		"/elsewhere/other.py": "failed",   // status 2
	}
	for file, wantOutcome := range want {
		if got[file] != wantOutcome {
			t.Errorf("%s outcome = %q, want %q", file, got[file], wantOutcome)
		}
	}
}

// The status vocabulary, asserted directly. Each value is grounded in what
// the rows carrying it contained — see outcomeFor.
func TestOutcomeForMapsObservedStatuses(t *testing.T) {
	tests := []struct {
		status int
		want   string
		note   string
	}{
		{3, "accepted", "215 rows measured"},
		{7, "rejected", `6 rows, error_details "Permission denied" — a human declining`},
		{6, "failed", `1 row, error_details "context cancelled"`},
		{2, "failed", "1 row, no error_details"},
		{0, "unknown", "never observed; must not be guessed at"},
		{99, "unknown", "an unrecognised status is a gap in what we know, not an error (NAV-21)"},
	}
	for _, tc := range tests {
		if got := outcomeFor(tc.status); got != tc.want {
			t.Errorf("outcomeFor(%d) = %q, want %q (%s)", tc.status, got, tc.want, tc.note)
		}
	}
}

// A rejection is what an acceptance rate is measured against, so it must
// not be collapsed into the same bucket as a tool error. This is the whole
// reason the funnel's acceptance stage was blank for agy.
func TestRejectedIsDistinctFromFailed(t *testing.T) {
	if outcomeFor(statusRejected) == outcomeFor(statusFailed) {
		t.Fatal("rejected and failed map to the same outcome — an acceptance rate cannot be computed")
	}
}

// A conversation belongs to a repository when it edited a file inside it,
// since agy records no workspace directory this adapter can trust.
func TestSessionFilesMatchesByEditedPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHODUNIT_AGY_PATH", root)

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "conv.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// The fixture edits /repo/... and /elsewhere/... — neither exists on
	// disk, which is the point: matching is on the recorded path.
	got, err := SessionFiles("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d conversations for /repo, want 1", len(got))
	}

	none, err := SessionFiles("/somewhere-else")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("got %d conversations for an unrelated repo", len(none))
	}
}

// Codex and Claude Code both file sessions per repository; agy does not, so
// a conversation that edited another project must not leak into this one.
func TestEditsOutsideTheRepoAreStillRecorded(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var outside bool
	for _, e := range entries {
		if strings.HasPrefix(e.File, "/elsewhere/") {
			outside = true
		}
	}
	// ParseSince reports everything the conversation did; scoping to a
	// repository is SessionFiles' job, and the journal is keyed by repo id
	// separately. This documents that split rather than asserting a filter
	// that does not exist here.
	if !outside {
		t.Fatal("expected ParseSince to report every edit in the conversation")
	}
}

func TestParseSessionActivityCountsSteps(t *testing.T) {
	acts, err := ParseSessionActivity(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("got %d sessions, want 1", len(acts))
	}
	if acts[0].ToolCalls != 8 {
		t.Errorf("tool calls = %d, want 8 (every step)", acts[0].ToolCalls)
	}
	if acts[0].Agent != AgentName {
		t.Errorf("agent = %q", acts[0].Agent)
	}
}

// Go's regexp caps a bounded repeat at 1000 characters. A written file body
// is routinely longer, so a regex-based extractor would silently miss the
// largest edits — exactly the ones that matter most.
func TestFindJSONObjectsHandlesLargePayloads(t *testing.T) {
	body := strings.Repeat("x = 1\n", 500) // ~3000 bytes
	obj, err := json.Marshal(map[string]any{
		"TargetFile":  "/repo/big.py",
		"CodeContent": body,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("\x12\x08framing"), obj...)

	found := findJSONObjects(payload)
	if len(found) != 1 {
		t.Fatalf("found %d objects in a large payload, want 1", len(found))
	}
	var a toolArgs
	if err := json.Unmarshal(found[0], &a); err != nil {
		t.Fatalf("large object did not parse: %v", err)
	}
	if a.CodeContent != body {
		t.Fatal("large content was truncated")
	}
}

// A brace inside written code must not end the object early.
func TestFindJSONObjectsHandlesBracesInContent(t *testing.T) {
	obj, err := json.Marshal(map[string]any{
		"TargetFile":  "/repo/a.go",
		"CodeContent": "func main() {\n\tif x { y() }\n}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := findJSONObjects(obj)
	if len(found) != 1 {
		t.Fatalf("found %d objects, want 1", len(found))
	}
	var a toolArgs
	if err := json.Unmarshal(found[0], &a); err != nil {
		t.Fatalf("object with braces in content did not parse: %v", err)
	}
	if !strings.Contains(a.CodeContent, "if x { y() }") {
		t.Fatalf("content truncated at an inner brace: %q", a.CodeContent)
	}
}

// A truncated payload yields nothing rather than a partial object.
func TestFindJSONObjectsIgnoresTruncated(t *testing.T) {
	if got := findJSONObjects([]byte(`{"TargetFile":"/a.py","CodeCon`)); len(got) != 0 {
		t.Fatalf("truncated payload yielded %d objects", len(got))
	}
}

// agy not installed is a fact, not an error.
func TestSessionFilesOnMissingRootIsNotAnError(t *testing.T) {
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "nope"))
	files, err := SessionFiles("/repo")
	if err != nil {
		t.Fatalf("missing conversations root returned an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files from a missing root", len(files))
	}
}

// A corrupt or locked conversation degrades to no entries for that one
// conversation rather than failing the ingest.
func TestCorruptDatabaseDegradesQuietly(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(bad, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := ParseSince(bad, time.Time{})
	if err != nil {
		t.Fatalf("a corrupt conversation returned an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a corrupt conversation produced %d entries", len(entries))
	}
}
