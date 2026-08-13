package repoid

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// outputIn runs git in dir and returns its stdout.
func outputIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runIn(t, dir, "init", "-q")
	return dir
}

func runIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitIn(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(t, dir, "add", name)
	runIn(t, dir, "commit", "-q", "-m", "add "+name)
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
}

func TestForCurrentRepoReturnsRootCommit(t *testing.T) {
	dir := newRepo(t)
	commitIn(t, dir, "a.txt")
	commitIn(t, dir, "b.txt")
	chdir(t, dir)

	id, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}

	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list: %v", err)
	}
	want := string(out[:40])
	if id != want {
		t.Errorf("ForCurrentRepo() = %q, want the root commit %q", id, want)
	}
}

func TestIDIsStableAsHistoryGrows(t *testing.T) {
	// The identifier must not change as commits are added — otherwise every
	// journal row written yesterday belongs to a different "repo" today.
	dir := newRepo(t)
	commitIn(t, dir, "a.txt")
	chdir(t, dir)

	first, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}

	commitIn(t, dir, "b.txt")
	commitIn(t, dir, "c.txt")

	after, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}
	if after != first {
		t.Errorf("identifier changed as history grew: %q then %q", first, after)
	}
}

func TestCloneAtADifferentPathHasTheSameID(t *testing.T) {
	// The point of using the root commit rather than a filesystem path:
	// the same repository is the same repository wherever it is checked out.
	origin := newRepo(t)
	commitIn(t, origin, "a.txt")
	chdir(t, origin)
	want, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}

	chdir(t, clone)
	got, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo in clone: %v", err)
	}
	if got != want {
		t.Errorf("clone at a different path got %q, want %q", got, want)
	}
}

func TestUnrelatedReposHaveDifferentIDs(t *testing.T) {
	a := newRepo(t)
	commitIn(t, a, "a.txt")
	chdir(t, a)
	idA, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}

	b := newRepo(t)
	commitIn(t, b, "b.txt")
	chdir(t, b)
	idB, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}

	if idA == idB {
		t.Errorf("two unrelated repositories share the identifier %q", idA)
	}
}

func TestRepoWithNoCommitsErrors(t *testing.T) {
	chdir(t, newRepo(t))
	if _, err := ForCurrentRepo(); err == nil {
		t.Error("ForCurrentRepo() on a repo with no commits = nil error, want an error")
	}
}

func TestMultipleRootsPickDeterministically(t *testing.T) {
	// Merging unrelated histories leaves several root commits. git lists
	// them in traversal order, which is not stable across clones, so the
	// choice has to be deterministic.
	main := newRepo(t)
	commitIn(t, main, "a.txt")

	other := newRepo(t)
	commitIn(t, other, "b.txt")

	runIn(t, main, "remote", "add", "other", other)
	runIn(t, main, "fetch", "-q", "other")
	// Ask the other repository what its branch is called rather than
	// assuming "main". git's default depends on its version and on
	// init.defaultBranch, so a hardcoded name passes on a machine
	// configured one way and fails on CI configured the other.
	branch := strings.TrimSpace(outputIn(t, other, "rev-parse", "--abbrev-ref", "HEAD"))
	runIn(t, main, "merge", "--allow-unrelated-histories", "-q", "-m", "merge unrelated", "other/"+branch)

	chdir(t, main)

	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list: %v", err)
	}
	if len(out) < 82 { // two 40-char shas plus newlines
		t.Skipf("expected two root commits, got: %q", out)
	}

	id, err := ForCurrentRepo()
	if err != nil {
		t.Fatalf("ForCurrentRepo: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := ForCurrentRepo()
		if err != nil {
			t.Fatalf("ForCurrentRepo: %v", err)
		}
		if again != id {
			t.Fatalf("multi-root repo gave different ids across calls: %q then %q", id, again)
		}
	}
}
