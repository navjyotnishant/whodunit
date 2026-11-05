package repoid

import (
	"os"
	"os/exec"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/repoid"
)

// The public id is the one dun uses, and resolving it writes nothing.
func TestForRepoMatchesDun(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	before, _ := os.ReadFile(dir + "/.git/config")
	got, err := ForRepo(dir)
	want, err2 := repoid.ForRepo(dir)
	if err != nil || err2 != nil || got == "" || got != want {
		t.Fatalf("ForRepo = %q (%v), dun = %q (%v)", got, err, want, err2)
	}
	after, _ := os.ReadFile(dir + "/.git/config")
	if string(before) != string(after) {
		t.Fatal("ForRepo changed .git/config")
	}
	if _, err := ForRepo(t.TempDir()); err == nil {
		t.Fatal("no error outside a repository")
	}
}
