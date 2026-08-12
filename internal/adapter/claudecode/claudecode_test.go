package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fixture = `{"type":"user","timestamp":"2026-08-11T22:49:00.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"do the thing"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:01.653Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"text","text":"working on it"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:09.451Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c1","name":"Write","input":{"file_path":"/repo/main.go","content":"package main\nfunc main() {}\n"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:09.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:20.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"c2","name":"Edit","input":{"file_path":"/repo/main.go","old_string":"func main() {}","new_string":"func main() {\n\tprintln(\"hi\")\n}"}}]}}
{"type":"user","timestamp":"2026-08-11T22:50:20.500Z","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"c2","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-11T22:50:30.000Z","sessionId":"sess-1","version":"2.1.227","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}
`

func TestParseSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, err := ParseSince(path, time.Time{})
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
	entries, err := ParseSince(path, cutoff)
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

	entries, err := ParseSince(path, time.Time{})
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
