// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The pre-adoption baseline `dun init` captures, and refuses to
// overwrite.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The measurement has one opportunity. Once the hooks stamp a commit the
// unassisted population stops growing, so the snapshot has to be taken
// before they are installed — not merely during init.
func TestInitCapturesABaselineBeforeInstallingHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	repo := newRepo(t, "with-history")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(newStatusTestCmd(&out), ""); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(home, "baselines", "*.json"))
	if len(files) != 1 {
		t.Fatalf("got %d baseline files, want 1\n%s", len(files), out.String())
	}

	// Ordering is the whole point, and the output is where it is visible.
	s := out.String()
	bi := strings.Index(s, "pre-adoption baseline")
	hi := strings.Index(s, "installed prepare-commit-msg")
	if bi < 0 || hi < 0 {
		t.Fatalf("expected both a baseline line and a hook line:\n%s", s)
	}
	if bi > hi {
		t.Errorf("the baseline was captured after hooks were installed:\n%s", s)
	}
}

// A re-run of init is routine — after an upgrade, or to repair hooks — and
// happens long after adoption. Rewriting the snapshot then would replace a
// genuine pre-adoption measurement with one taken from a window full of
// assisted commits: valid-looking, and comparing AI against itself.
func TestInitNeverOverwritesAnExistingBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	repo := newRepo(t, "rerun")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	if err := runInit(newStatusTestCmd(&bytes.Buffer{}), ""); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(home, "baselines", "*.json"))
	if len(files) != 1 {
		t.Fatalf("setup: got %d baselines, want 1", len(files))
	}
	first, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}

	// Work happens, then someone re-runs init.
	if err := os.WriteFile(filepath.Join(repo, "later.txt"),
		[]byte("written after adoption\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "later.txt")
	git(t, repo, "commit", "-q", "-m", "work done after adoption")
	if err := runInit(newStatusTestCmd(&bytes.Buffer{}), ""); err != nil {
		t.Fatal(err)
	}

	second, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("a second init rewrote the baseline; the pre-adoption window " +
			"cannot be re-measured once attribution has started")
	}

	// Comparing bytes is not enough on its own: a re-capture over the same
	// window produces nearly identical content, so the commit count is what
	// actually distinguishes a preserved snapshot from a rewritten one.
	var snap struct {
		Git struct {
			Commits int `json:"commits"`
		} `json:"git"`
	}
	if err := json.Unmarshal(second, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Git.Commits != 1 {
		t.Errorf("baseline now reports %d commits, want the 1 it was created "+
			"with — the later commit has been folded into a measurement that "+
			"is supposed to predate it", snap.Git.Commits)
	}
}

// A repository with no commits has no identifier and no history to measure.
// Instrumenting it must still work — the hooks are the job.
func TestInitWithoutCommitsStillInstallsHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	dir := t.TempDir()
	git(t, dir, "init", "-q")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(newStatusTestCmd(&out), ""); err != nil {
		t.Fatalf("init failed on a repository with no commits: %v", err)
	}
	if !strings.Contains(out.String(), "installed prepare-commit-msg") {
		t.Errorf("hooks were not installed:\n%s", out.String())
	}
	files, _ := filepath.Glob(filepath.Join(home, "baselines", "*.json"))
	if len(files) != 0 {
		t.Errorf("wrote a baseline for a repository with no history: %v", files)
	}
}
