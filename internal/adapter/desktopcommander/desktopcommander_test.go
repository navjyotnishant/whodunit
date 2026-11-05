package desktopcommander

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

// setup makes a repository, a second folder, and an empty history
// directory, and returns a function that writes the history.
func setup(t *testing.T) (repo, other string, write func([]map[string]any)) {
	t.Helper()
	base := t.TempDir()
	repo = linehash.Canonical(filepath.Join(base, "repo"))
	other = linehash.Canonical(filepath.Join(base, "other"))
	for _, d := range []string{repo, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(base, "dc")
	t.Setenv(adapter.EnvVarFor(AgentName), dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write = func(lines []map[string]any) { writeHistory(t, dir, lines) }
	return repo, other, write
}

func writeHistory(t *testing.T, dir string, lines []map[string]any) {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	sb.WriteString("not json\n")
	if err := os.WriteFile(filepath.Join(dir, historyFile), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ok() map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": "Successfully wrote"}}}
}

func call(tool string, args map[string]any, out map[string]any) map[string]any {
	m := map[string]any{"timestamp": "2026-11-05T12:00:00.000Z", "toolName": tool, "arguments": args}
	if out != nil {
		m["output"] = out
	}
	return m
}

func TestEditsInsideTheRepositoryAreHashed(t *testing.T) {
	repo, other, write := setup(t)
	write([]map[string]any{
		call("write_file", map[string]any{"path": repo + "/a.go", "content": "package a\n\nfunc A() {}\n", "mode": "rewrite"}, ok()),
		call("edit_block", map[string]any{"file_path": repo + "/b.go", "old_string": "x := 1", "new_string": "x := 2\ny := 3"}, ok()),
		call("write_file", map[string]any{"path": other + "/c.go", "content": "package c\n"}, ok()),
		call("write_file", map[string]any{"path": repo + "/d.go", "content": "package d\n"}, map[string]any{"isError": true}),
		call("edit_block", map[string]any{"file_path": repo + "/s.xlsx", "range": "Sheet1!A1:A1", "content": [][]string{{"1"}}}, ok()),
		call("read_file", map[string]any{"path": repo + "/a.go"}, ok()),
		call("start_process", map[string]any{"command": "cat > " + repo + "/gen.go <<'EOF'\npackage gen\nEOF"}, ok()),
	})

	files, err := SessionFiles(repo)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
	entries, err := ParseSince(files[0], time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range entries {
		if e.Agent != AgentName {
			t.Errorf("agent = %q", e.Agent)
		}
		if !strings.HasPrefix(e.File, repo+"/") {
			t.Errorf("entry outside the repository: %s", e.File)
		}
		got[filepath.Base(e.File)] = len(e.LineHashes)
	}
	if got["a.go"] == 0 || got["b.go"] == 0 {
		t.Errorf("accepted edits carry no line hashes: %v", got)
	}
	if n, seen := got["d.go"]; !seen || n != 0 {
		t.Errorf("failed write: seen=%v hashes=%d, want seen with none", seen, n)
	}
	if _, seen := got["s.xlsx"]; seen {
		t.Errorf("spreadsheet range edit recorded")
	}
	if n, seen := got["gen.go"]; !seen || n != 0 {
		t.Errorf("shell write: seen=%v hashes=%d, want named without hashes", seen, n)
	}
}

func TestHistoryThatNeverNamesTheRepositoryIsNotListed(t *testing.T) {
	repo, other, write := setup(t)
	write([]map[string]any{
		call("write_file", map[string]any{"path": other + "/c.go", "content": "package c\n"}, ok()),
		// Visiting the repository is not editing it.
		call("start_process", map[string]any{"command": "cd " + repo + "/ && git status"}, ok()),
		call("read_file", map[string]any{"path": repo + "/main.go"}, ok()),
	})
	files, err := SessionFiles(repo)
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %v, err = %v: a history that never names the repository must not count as searched", files, err)
	}
}

func TestNoHistoryMeansNothing(t *testing.T) {
	t.Setenv(adapter.EnvVarFor(AgentName), filepath.Join(t.TempDir(), "absent"))
	files, err := SessionFiles(t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
}
