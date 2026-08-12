package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/repoid"
)

// gitDir returns the .git directory for the current repo, resolving worktrees
// and core.hooksPath-style setups via git itself rather than assuming layout.
func gitDir() (string, error) {
	return gitDirFor("")
}

// gitDirFor returns the .git directory for the repository at dir. An empty
// dir means the current working directory. The path is made absolute:
// git reports a relative one when asked from inside the repo, which would
// break as soon as the caller resolves it from anywhere else.
func gitDirFor(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or git not on PATH): %w", err)
	}

	gd := strings.TrimSpace(string(out))
	if filepath.IsAbs(gd) {
		return gd, nil
	}
	base := dir
	if base == "" {
		if base, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, gd), nil
}

// journalDataDir returns the directory holding the global journal database.
// The journal is global and scoped by repo id rather than one file per
// repo, so the same code path works whether the backend is the embedded
// SQLite file or, later, a shared server.
func journalDataDir() (string, error) {
	return config.DataDir()
}

// currentRepoID returns the stable identifier for the repository in the
// current working directory.
func currentRepoID() (string, error) {
	return repoid.ForCurrentRepo()
}

// defaultBaselinePath returns where this repository's baseline snapshot
// lives. It is kept outside the repo: a baseline measures a window that
// cannot be recaptured, and anything under .git/ dies with a fresh clone
// or a `git clean -xfd`.
func defaultBaselinePath() (string, error) {
	dir, err := config.BaselinesDir()
	if err != nil {
		return "", err
	}
	repoID, err := currentRepoID()
	if err != nil {
		return "", err
	}
	if err := config.EnsureDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, repoID+".json"), nil
}
