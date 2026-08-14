// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: dun status outside a repository, and with --repo.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/spf13/cobra"
)

// NAV-74 criterion 1. Run outside a repository, status used to fail with
// git's own error — "read git log: exit status 128" — which says nothing
// about what to do next.
func TestStatusOutsideARepositoryListsInstrumentedOnes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	repo := newRepo(t, "listed")
	if err := registry.Add("repo-id-1", repo, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Somewhere that is definitely not a git repository.
	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(newStatusTestCmd(&out), ""); err != nil {
		t.Fatalf("status outside a repository failed: %v", err)
	}
	if !strings.Contains(out.String(), "listed") {
		t.Fatalf("the instrumented repository was not listed:\n%s", out.String())
	}
}

// Criterion 5: an empty list with no explanation reads like a bug.
func TestStatusWithNothingInstrumentedExplainsItself(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(newStatusTestCmd(&out), ""); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "no repositories are instrumented") {
		t.Errorf("empty registry did not say so:\n%s", s)
	}
	if !strings.Contains(s, "dun init") {
		t.Errorf("empty registry did not name the command to fix it:\n%s", s)
	}
}

// Criterion 6. A repository can move after `dun init` recorded it. Its
// journal rows survive, so dropping the row would make real recorded
// history look like it was never instrumented.
func TestStatusShowsMovedRepositoriesRatherThanHidingThem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	gone := filepath.Join(t.TempDir(), "gone-away")
	if err := registry.Add("repo-id-gone", gone, time.Now()); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(newStatusTestCmd(&out), ""); err != nil {
		t.Fatalf("a missing repository failed the whole listing: %v", err)
	}
	if !strings.Contains(out.String(), "moved or deleted") {
		t.Fatalf("a missing repository was not reported as such:\n%s", out.String())
	}
}

// Criterion 3: --repo reports a specific repository from anywhere.
func TestStatusRepoFlagWorksFromAnywhere(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	repo := newRepo(t, "target")
	// A commit carrying a trailer, so coverage is non-zero and the report
	// is distinguishable from an empty one.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "feat: thing\n\nAI-Attribution: status=assisted; method=observed; agent=claude-code")

	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(newStatusTestCmd(&out), repo); err != nil {
		t.Fatalf("status --repo failed: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "observed") {
		t.Fatalf("--repo did not report the target repository's method mix:\n%s", s)
	}
}

// Criterion 4: the same resolver as `dun journal --repo`, so the two
// commands fail the same way on the same input.
func TestStatusRepoFlagErrorsAreNamed(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	notARepo := t.TempDir()
	cases := []struct{ path, want string }{
		{filepath.Join(notARepo, "nope"), "no such directory"},
		{notARepo, "not a git repository"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := runStatus(newStatusTestCmd(&out), c.path)
		if err == nil {
			t.Fatalf("--repo %s returned no error", c.path)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not mention %q", err, c.want)
		}
	}
}

// Criterion 2: inside a repository, behaviour is unchanged.
func TestStatusInsideARepositoryIsUnchanged(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	repo := newRepo(t, "inside")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(newStatusTestCmd(&out), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "commits examined") {
		t.Fatalf("inside a repository, status did not report it:\n%s", out.String())
	}
	// Not the cross-repo listing.
	if strings.Contains(out.String(), "instrumented repositor") {
		t.Fatalf("inside a repository, status listed every repository:\n%s", out.String())
	}
}

func TestShortRepoName(t *testing.T) {
	cases := map[string]string{
		"/Users/me/code/whodunit":  "code/whodunit",
		"/Users/me/src/org/thing":  "org/thing",
		"/tmp":                     "/tmp",
		"/Users/me/code/whodunit/": "code/whodunit",
	}
	for in, want := range cases {
		if got := shortRepoName(in); got != want {
			t.Errorf("shortRepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// newStatusTestCmd returns a cobra command writing to buf, so runStatus can
// be exercised without a real terminal.
func newStatusTestCmd(buf *bytes.Buffer) *cobra.Command {
	cmd := newStatusCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd
}
