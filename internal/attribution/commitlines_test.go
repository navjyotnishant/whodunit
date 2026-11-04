package attribution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/linehash"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null",
		"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedLinesMatchTheStagedHashes(t *testing.T) {
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "diff.noprefix", "true") // must not hide paths

	writeT(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() int {\n\treturn 1\n}\n")
	gitT(t, root, "add", "-A")
	staged := map[uint64]bool{}
	for _, h := range stagedLineHashes(gitT(t, root, "diff", "--cached", "--unified=0", "--src-prefix=a/", "--dst-prefix=b/"), root) {
		staged[h] = true
	}
	gitT(t, root, "commit", "-q", "-m", "root")
	first := gitT(t, root, "rev-parse", "HEAD")

	writeT(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() int {\n\treturn 2\n}\n")
	writeT(t, filepath.Join(root, "b.go"), "package main\nvar x = 1\nvar x = 1\n}\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "second")
	second := gitT(t, root, "rev-parse", "HEAD")

	got, err := CommittedLines(root, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}

	if len(got[first]) != len(staged) {
		t.Fatalf("root commit: %d lines, staged diff had %d", len(got[first]), len(staged))
	}
	for _, l := range got[first] {
		if !staged[l.Hash] {
			t.Errorf("root commit line %+v does not match the staged hash", l)
		}
		if l.Path != "pkg/a.go" {
			t.Errorf("path = %q, want pkg/a.go", l.Path)
		}
	}

	// "return 2" in pkg/a.go, and "package main" plus one "var x = 1" in
	// b.go: the repeat and the lone brace are dropped.
	if n := len(got[second]); n != 3 {
		t.Fatalf("second commit: %d lines %+v, want 3", n, got[second])
	}
	want := linehash.Of(linehash.Canonical(filepath.Join(root, "pkg", "a.go")), "\treturn 2")
	found := false
	for _, l := range got[second] {
		found = found || (l.Hash == want && l.Path == "pkg/a.go")
	}
	if !found {
		t.Error("changed line in pkg/a.go missing or hashed differently from the hook")
	}
}

func TestCommittedLinesMergeUsesFirstParent(t *testing.T) {
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	writeT(t, filepath.Join(root, "m.go"), "package main\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "root")
	gitT(t, root, "checkout", "-q", "-b", "feature")
	writeT(t, filepath.Join(root, "f.go"), "func feature() {}\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "feature")
	gitT(t, root, "checkout", "-q", "main")
	writeT(t, filepath.Join(root, "n.go"), "func onMain() {}\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "main work")
	gitT(t, root, "merge", "-q", "--no-ff", "-m", "merge", "feature")
	merge := gitT(t, root, "rev-parse", "HEAD")

	got, err := CommittedLines(root, []string{merge})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[merge]) != 1 || got[merge][0].Path != "f.go" {
		t.Fatalf("merge lines = %+v, want only the feature branch's f.go line", got[merge])
	}
}

func TestCommittedLinesEmptyAndBadInput(t *testing.T) {
	if got, err := CommittedLines(t.TempDir(), nil); err != nil || len(got) != 0 {
		t.Fatalf("no shas: %v, %v", got, err)
	}
	root := t.TempDir()
	gitT(t, root, "init", "-q")
	if _, err := CommittedLines(root, []string{"0123456789012345678901234567890123456789"}); err == nil {
		t.Fatal("unknown sha accepted")
	}
}
