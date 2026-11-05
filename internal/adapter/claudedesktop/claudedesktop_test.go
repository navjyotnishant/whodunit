package claudedesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// layout builds a fake Claude Desktop store with two sessions: one with the
// repository connected, one with an unrelated folder. It returns the
// repository path and the first session's transcript.
func layout(t *testing.T, mountNames bool) (repo, transcript string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "store")
	repo = filepath.Join(base, "work", "repo")
	other := filepath.Join(base, "work", "notes")
	for _, d := range []string{repo, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(adapter.EnvVarFor(AgentName), root)

	org := filepath.Join(root, "acct", "org")
	meta := map[string]any{
		"cwd":                 filepath.Join(org, "local_a", "outputs"),
		"userSelectedFolders": []string{repo},
		"model":               "claude-sonnet-5-5",
	}
	if mountNames {
		meta["folderMountNames"] = map[string]string{repo: "repo"}
	}
	writeJSON(t, filepath.Join(org, "local_a.json"), meta)
	writeJSON(t, filepath.Join(org, "local_b.json"), map[string]any{"userSelectedFolders": []string{other}})

	transcript = filepath.Join(org, "local_a", ".claude", "projects", "-x-outputs", "s1.jsonl")
	other2 := filepath.Join(org, "local_b", ".claude", "projects", "-y", "s2.jsonl")
	ts := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lines := []map[string]any{
		{"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.300", "cwd": filepath.Join(org, "local_a", "outputs"),
			"message": map[string]any{"id": "m1", "model": "claude-sonnet-5-5", "content": []map[string]any{
				{"type": "tool_use", "id": "t1", "name": "Write", "input": map[string]any{"file_path": "/sessions/lucid-x/mnt/repo/main.go", "content": "package main\n\nfunc main() {}\n"}},
				{"type": "tool_use", "id": "t2", "name": "Write", "input": map[string]any{"file_path": "/sessions/lucid-x/mnt/outputs/report.md", "content": "# report\n"}},
				{"type": "tool_use", "id": "t3", "name": "mcp__workspace__bash", "input": map[string]any{"command": "cat > /sessions/lucid-x/mnt/repo/gen.go <<'EOF'\npackage main\nEOF"}},
				{"type": "tool_use", "id": "t4", "name": "Edit", "input": map[string]any{"file_path": "/tmp/scratch.py", "old_string": "a", "new_string": "b"}},
			}}},
		{"type": "user", "timestamp": ts, "sessionId": "s1", "message": map[string]any{"content": []map[string]any{
			{"type": "tool_result", "tool_use_id": "t1", "content": "File created"},
			{"type": "tool_result", "tool_use_id": "t2", "content": "File created"},
			{"type": "tool_result", "tool_use_id": "t3", "content": ""},
			{"type": "tool_result", "tool_use_id": "t4", "content": "ok"},
		}}},
	}
	writeLines(t, transcript, lines)
	writeLines(t, other2, lines[:1])
	return repo, transcript
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLines(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionFilesFindsOnlyConnectedSessions(t *testing.T) {
	repo, transcript := layout(t, true)
	files, err := SessionFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != transcript {
		t.Fatalf("files = %v, want only %s", files, transcript)
	}

	// A subdirectory of the connected folder is the same repository.
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if files, _ := SessionFiles(sub); len(files) != 1 {
		t.Fatalf("subdirectory: files = %v, want 1", files)
	}

	// A sibling whose name extends the repository's is not inside it.
	if files, _ := SessionFiles(repo + "-old"); len(files) != 0 {
		t.Fatalf("sibling: files = %v, want none", files)
	}
}

func TestParseSinceMapsVMPathsAndDropsTheRest(t *testing.T) {
	for _, mountNames := range []bool{true, false} {
		repo, transcript := layout(t, mountNames)
		entries, err := ParseSince(transcript, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]bool{}
		for _, e := range entries {
			if e.Agent != AgentName {
				t.Errorf("agent = %q, want %q", e.Agent, AgentName)
			}
			if e.File != "" {
				files[e.File] = true
			}
		}
		main := filepath.Join(linehash.Canonical(repo), "main.go")
		gen := filepath.Join(linehash.Canonical(repo), "gen.go")
		if !files[main] {
			t.Errorf("mountNames=%v: no entry for %s in %v", mountNames, main, files)
		}
		if !files[gen] {
			t.Errorf("mountNames=%v: no bash entry for %s in %v", mountNames, gen, files)
		}
		if len(files) != 2 {
			t.Errorf("mountNames=%v: files = %v, want only the two repository files", mountNames, files)
		}
		for _, e := range entries {
			if e.File == main && len(e.LineHashes) == 0 {
				t.Errorf("Write to %s carries no line hashes", main)
			}
		}
	}
}

func TestEditsToOtherConnectedFoldersAreScopedOut(t *testing.T) {
	repo, transcript := layout(t, true)
	other := filepath.Join(filepath.Dir(repo), "notes")

	// Connect a second folder to the same session and write to it.
	meta := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(transcript)))) + ".json")
	writeJSON(t, meta, map[string]any{
		"userSelectedFolders": []string{repo, other},
		"folderMountNames":    map[string]string{repo: "repo", other: "notes"},
	})
	ts := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	writeLines(t, transcript, []map[string]any{
		{"type": "assistant", "timestamp": ts, "sessionId": "s1", "message": map[string]any{"id": "m1", "content": []map[string]any{
			{"type": "tool_use", "id": "t1", "name": "Write", "input": map[string]any{"file_path": "/sessions/x/mnt/repo/a.go", "content": "package a\n"}},
			{"type": "tool_use", "id": "t2", "name": "Write", "input": map[string]any{"file_path": "/sessions/x/mnt/notes/b.md", "content": "# b\n"}},
		}}},
		{"type": "user", "timestamp": ts, "sessionId": "s1", "message": map[string]any{"content": []map[string]any{
			{"type": "tool_result", "tool_use_id": "t1", "content": "ok"},
			{"type": "tool_result", "tool_use_id": "t2", "content": "ok"},
		}}},
	})

	if _, err := SessionFiles(repo); err != nil {
		t.Fatal(err)
	}
	entries, err := ParseSince(transcript, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.File != "" && e.File != filepath.Join(linehash.Canonical(repo), "a.go") {
			t.Errorf("entry for %s journaled against %s", e.File, repo)
		}
	}
}

// A Cowork session is often not coding. Its other tool calls, and any
// session that wrote nothing in the repository, must not reach the journal.
func TestOnlyCodeWorkIsRecorded(t *testing.T) {
	repo, transcript := layout(t, true)
	ts := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	writeLines(t, transcript, []map[string]any{
		// A session that only sent email and read a CRM.
		{"type": "user", "timestamp": ts, "sessionId": "chat", "message": map[string]any{"content": "draft a reply to the vendor"}},
		{"type": "assistant", "timestamp": ts, "sessionId": "chat", "message": map[string]any{"id": "c1", "content": []map[string]any{
			{"type": "text", "text": "Here is a draft."},
			{"type": "tool_use", "id": "g1", "name": "mcp__Gmail__send_message", "input": map[string]any{"to": "x@example.com"}},
			{"type": "tool_use", "id": "w1", "name": "Write", "input": map[string]any{"file_path": "/sessions/x/mnt/outputs/reply.md", "content": "Hi\n"}},
		}}},
		// A session that also wrote code here.
		{"type": "assistant", "timestamp": ts, "sessionId": "code", "message": map[string]any{"id": "k1", "content": []map[string]any{
			{"type": "tool_use", "id": "e1", "name": "mcp__EngageHub__list_contacts", "input": map[string]any{}},
			{"type": "tool_use", "id": "w2", "name": "Write", "input": map[string]any{"file_path": "/sessions/x/mnt/repo/a.go", "content": "package a\n"}},
		}}},
		{"type": "user", "timestamp": ts, "sessionId": "code", "message": map[string]any{"content": []map[string]any{
			{"type": "tool_result", "tool_use_id": "w2", "content": "ok"},
		}}},
	})
	if _, err := SessionFiles(repo); err != nil {
		t.Fatal(err)
	}

	entries, err := ParseSince(transcript, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].File != filepath.Join(linehash.Canonical(repo), "a.go") {
		t.Fatalf("entries = %+v, want only the write to a.go", entries)
	}
	for _, e := range entries {
		if e.Event == "tool_call" || e.MCPServer != "" || strings.HasPrefix(e.Tool, "mcp__") {
			t.Errorf("non-code call recorded: %+v", e)
		}
	}

	sessions, err := ParseSessionActivity(transcript, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Session != "code" {
		t.Fatalf("sessions = %+v, want only the session that wrote code here", sessions)
	}
}

func TestAmbiguousBaseNamesAreNotMapped(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a", "repo"), filepath.Join(base, "b", "repo")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := filepath.Join(base, "local_z.json")
	writeJSON(t, meta, map[string]any{"userSelectedFolders": []string{a, b}})
	m, err := loadMounts(meta)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := m.hostPath("/sessions/s/mnt/repo/x.go"); ok {
		t.Fatalf("ambiguous mount mapped to %s", p)
	}
	// A host path inside a connected folder is still kept as written.
	want := filepath.Join(linehash.Canonical(b), "x.go")
	if p, ok := m.hostPath(filepath.Join(b, "x.go")); !ok || filepath.Clean(p) != filepath.Clean(want) {
		t.Fatalf("host path = %q, %v", p, ok)
	}
}

func TestSessionActivityIsLabelledClaudeDesktop(t *testing.T) {
	_, transcript := layout(t, true)
	sessions, err := ParseSessionActivity(transcript, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Agent != AgentName {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestNoStoreMeansNoSessions(t *testing.T) {
	t.Setenv(adapter.EnvVarFor(AgentName), filepath.Join(t.TempDir(), "absent"))
	files, err := SessionFiles(t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
}
