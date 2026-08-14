package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

const fixture = `{"type":"user","timestamp":"2026-08-11T22:49:00.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"do the thing"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:01.653Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"working on it"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:09.451Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c1","name":"Write","input":{"file_path":"/repo/main.go","content":"package main\nfunc main() {}\n"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:09.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:20.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c2","name":"Edit","input":{"file_path":"/repo/main.go","old_string":"func main() {}","new_string":"func main() {\n\tprintln(\"hi\")\n}"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:20.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c2","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:30.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}
`

// edits keeps only file-edit entries. ParseSince also emits a bare
// tool_call entry per non-editing call, which these tests are not about.
func edits(entries []journal.Entry) []journal.Entry {
	var out []journal.Entry
	for _, e := range entries {
		if e.Event == "tool_use" {
			out = append(out, e)
		}
	}
	return out
}

// parseEdits is ParseSince narrowed to file edits.
func parseEdits(path string, since time.Time) ([]journal.Entry, error) {
	all, err := ParseSince(path, since)
	if err != nil {
		return nil, err
	}
	return edits(all), nil
}

func TestParseSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, err := parseEdits(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 tool_use entries (Bash and text excluded), got %d: %+v", len(entries), entries)
	}

	write := entries[0]
	if write.Tool != "Write" || write.File != "/repo/main.go" || write.LinesAdded != 3 {
		t.Errorf("Write entry wrong: %+v", write)
	}
	if write.Agent != AgentName || write.AgentVersion != "2.1.227" || write.Session != "sess-1" {
		t.Errorf("Write entry metadata wrong: %+v", write)
	}
	if write.HunkHash == "" {
		t.Error("Write entry missing hunk hash")
	}

	edit := entries[1]
	if edit.Tool != "Edit" || edit.LinesRemoved != 1 || edit.LinesAdded != 3 {
		t.Errorf("Edit entry wrong: %+v", edit)
	}
	if edit.HunkHash == write.HunkHash {
		t.Error("Edit and Write hunk hashes must differ")
	}
}

func TestParseSinceFiltersByTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cutoff := time.Date(2026, 8, 11, 22, 50, 15, 0, time.UTC)
	entries, err := parseEdits(path, cutoff)
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 1 || entries[0].Tool != "Edit" {
		t.Fatalf("want only the post-cutoff Edit entry, got %+v", entries)
	}
}

func TestParseSinceSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	content := "not json at all\n" + fixture
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, err := parseEdits(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince should not fail on malformed line: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries despite leading garbage line, got %d", len(entries))
	}
}

func TestSlugForCwd(t *testing.T) {
	got := SlugForCwd("/Users/nav/repo")
	want := "-Users-nav-repo"
	if got != want {
		t.Errorf("SlugForCwd = %q, want %q", got, want)
	}
}

func TestSlugForCwdIsALegalDirectoryName(t *testing.T) {
	// A slug is used as a directory name, so it has to be creatable. The
	// Windows path here produced "C:-Users-runneradmin-..." when only the
	// separator was replaced, and every mkdir failed with "The directory
	// name is invalid" — which surfaced as no transcripts found and every
	// commit undetermined, rather than as an error anyone could see.
	//
	// Asserted on every platform, not just Windows: the encoding has to be
	// the same everywhere, because a path with forward slashes reaches this
	// function on Windows too.
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"unix", "/Users/nav/repo", "-Users-nav-repo"},
		{"windows backslash", `C:\Users\nav\repo`, "C-Users-nav-repo"},
		{"windows forward slash", "C:/Users/nav/repo", "C-Users-nav-repo"},
		{"unc", `\\server\share\repo`, "--server-share-repo"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SlugForCwd(c.cwd)
			if got != c.want {
				t.Errorf("SlugForCwd(%q) = %q, want %q", c.cwd, got, c.want)
			}
			if strings.ContainsAny(got, `:\/`) {
				t.Errorf("SlugForCwd(%q) = %q, which cannot be a directory "+
					"name on Windows", c.cwd, got)
			}
			// The real proof: the operating system accepts it.
			if err := os.MkdirAll(filepath.Join(t.TempDir(), got), 0o700); err != nil {
				t.Errorf("cannot create a directory named %q: %v", got, err)
			}
		})
	}
}

func TestSlugForCwdDoesNotCollideAcrossDrives(t *testing.T) {
	// Dropping the colon rather than mapping it to '-' keeps C:\repo distinct
	// from a directory literally named "C-". Mapping it would merge two
	// repositories' transcripts under one slug.
	if SlugForCwd(`C:\repo`) == SlugForCwd(`C-\repo`) {
		t.Error("C:\\repo and C-\\repo produce the same slug")
	}
}
