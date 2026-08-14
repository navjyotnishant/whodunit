package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a git repo with one commit and returns its path.
func newRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	// The message carries the repo name so two repos created in the same
	// second get different root commit SHAs. Identical empty commits with
	// the same author and timestamp hash identically, which would make
	// two distinct repositories share a repo id.
	git(t, dir, "commit", "--allow-empty", "-q", "-m", "base "+name)
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
	)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Criterion 1: a path resolves regardless of the current directory.
func TestResolveRepoByPath(t *testing.T) {
	dir := newRepo(t, "alpha")
	id, label, err := resolveRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 40 {
		t.Fatalf("repo id %q is not a sha", id)
	}
	if label != dir {
		t.Fatalf("label %q, want %q", label, dir)
	}
}

// Criterion 2: no flag keeps the previous behaviour.
func TestResolveRepoEmptyUsesCurrentDir(t *testing.T) {
	dir := newRepo(t, "beta")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	got, label, err := resolveRepo("")
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := resolveRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cwd resolved to %q, --repo resolved to %q", got, want)
	}
	if label != "this repository" {
		t.Fatalf("label %q, want %q", label, "this repository")
	}
}

// Criterion 3: each failure names its own problem. Showing nothing, or one
// generic message, leaves the user unable to tell a wrong path from an
// empty journal.
func TestResolveRepoErrorsAreDistinct(t *testing.T) {
	notARepo := t.TempDir()

	empty := filepath.Join(t.TempDir(), "unborn")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, empty, "init", "-q") // a repo, but no commits

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, path, want string
	}{
		{"missing", filepath.Join(notARepo, "nope"), "no such directory"},
		{"not a repo", notARepo, "not a git repository"},
		{"no commits", empty, "no commits yet"},
		{"not a directory", file, "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveRepo(tc.path)
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A 40-char hex string is treated as an id, not a path — so a registered
// repo stays reachable by id after it moves.
func TestLooksLikeRepoID(t *testing.T) {
	yes := []string{
		"ab1ba8d53279decc46077e5d1071f87297e5cd5d",
		strings.ToUpper("ab1ba8d53279decc46077e5d1071f87297e5cd5d"),
	}
	for _, s := range yes {
		if !looksLikeRepoID(s) {
			t.Errorf("%q not recognized as a repo id", s)
		}
	}
	no := []string{
		"",
		"/some/path",
		"ab1ba8d5", // short
		"ab1ba8d53279decc46077e5d1071f87297e5cd5dd", // 41
		"zb1ba8d53279decc46077e5d1071f87297e5cd5d",  // non-hex
		"./ab1ba8d53279decc46077e5d1071f87297e5cd",  // path-ish
	}
	for _, s := range no {
		if looksLikeRepoID(s) {
			t.Errorf("%q wrongly treated as a repo id", s)
		}
	}
}

// An id resolves without touching the filesystem, which is the point: the
// path in the registry may be stale.
func TestResolveRepoByIDNeedsNoPath(t *testing.T) {
	id := "ab1ba8d53279decc46077e5d1071f87297e5cd5d"
	got, _, err := resolveRepo(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q, want %q", got, id)
	}
}
