package attribution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/pkg/linehash"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The keyed variant adds KeyedHash and changes nothing else; the default
// variant leaves KeyedHash zero, so dun's output is untouched.
func TestCommittedLinesKeyedOnlyAddsKeyedHash(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n\nfunc Answer() int { return 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "first")
	sha := gitIn(t, root, "rev-parse", "HEAD")

	plain, err := CommittedLines(root, []string{sha})
	if err != nil || len(plain[sha]) == 0 {
		t.Fatalf("CommittedLines: %v %v", plain, err)
	}
	for _, l := range plain[sha] {
		if l.KeyedHash != 0 {
			t.Fatalf("CommittedLines filled KeyedHash: %+v", l)
		}
	}

	key, err := linehash.NewKey(1, []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := CommittedLinesKeyed(root, []string{sha}, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyed[sha]) != len(plain[sha]) {
		t.Fatalf("keyed has %d lines, plain %d", len(keyed[sha]), len(plain[sha]))
	}
	want := linehash.OfKeyed(key, "src/a.go", "func Answer() int { return 42 }")
	found := false
	for i, l := range keyed[sha] {
		if l.Path != plain[sha][i].Path || l.Hash != plain[sha][i].Hash {
			t.Fatalf("line %d differs from CommittedLines: %+v vs %+v", i, l, plain[sha][i])
		}
		if l.KeyedHash == 0 {
			t.Fatalf("line %d has no keyed hash", i)
		}
		found = found || l.KeyedHash == want
	}
	if !found {
		t.Fatal("no line carries linehash.OfKeyed over its repo-relative path")
	}

	if _, err := CommittedLinesKeyed(root, []string{sha}, linehash.Key{}); err == nil {
		t.Fatal("zero key accepted")
	}
}
