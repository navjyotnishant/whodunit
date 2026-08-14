package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDirInRealRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	gd, err := gitDir()
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	if !strings.HasSuffix(gd, ".git") {
		t.Errorf("gitDir() = %q, want it to end in .git", gd)
	}
}

func TestGitDirOutsideRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if _, err := gitDir(); err == nil {
		t.Error("gitDir() = nil error outside a git repo, want error")
	}
}

func TestJournalDataDirIsGlobalNotPerRepo(t *testing.T) {
	chdirToTestRepo(t)

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatalf("journalDataDir: %v", err)
	}
	// The journal is a single global store scoped by repo id, so its
	// location must not depend on which repository we happen to be in.
	if strings.Contains(dataDir, ".git") {
		t.Errorf("journalDataDir() = %q, want a global path outside any repository", dataDir)
	}
	// Data belongs in its own subdirectory, not loose alongside config.
	if filepath.Base(dataDir) != "data" {
		t.Errorf("journalDataDir() = %q, want it under a data/ directory", dataDir)
	}
}

func TestCurrentRepoIDIsStableAndNotAPath(t *testing.T) {
	chdirToTestRepo(t)

	id, err := currentRepoID()
	if err != nil {
		t.Fatalf("currentRepoID: %v", err)
	}
	if len(id) != 40 {
		t.Errorf("currentRepoID() = %q, want a 40-character commit sha", id)
	}
	// A path or remote URL as the identifier would break across clones and
	// would record local or org-identifying detail in a shared store.
	if strings.Contains(id, "/") {
		t.Errorf("currentRepoID() = %q, want a commit sha rather than a path", id)
	}

	again, err := currentRepoID()
	if err != nil {
		t.Fatalf("currentRepoID (second call): %v", err)
	}
	if again != id {
		t.Errorf("currentRepoID is not stable: %q then %q", id, again)
	}
}

func TestCurrentRepoIDFailsWithoutCommits(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	// A repository with no commits has no root commit, so it has no stable
	// identity — better to say so than to invent one.
	if _, err := currentRepoID(); err == nil {
		t.Error("currentRepoID() on a repo with no commits = nil error, want an error")
	}
}

func TestDefaultBaselinePathIsOutsideTheRepo(t *testing.T) {
	chdirToTestRepo(t)

	p, err := defaultBaselinePath()
	if err != nil {
		t.Fatalf("defaultBaselinePath: %v", err)
	}
	// A baseline measures a window that cannot be recaptured; anything
	// under .git/ dies with a fresh clone or `git clean -xfd`.
	if strings.Contains(p, ".git") {
		t.Errorf("defaultBaselinePath() = %q, want it outside the repository", p)
	}
	if filepath.Base(filepath.Dir(p)) != "baselines" {
		t.Errorf("defaultBaselinePath() = %q, want it under a baselines directory", p)
	}
}
