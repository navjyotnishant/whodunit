package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitDir returns the .git directory for the current repo, resolving worktrees
// and core.hooksPath-style setups via git itself rather than assuming layout.
func gitDir() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or git not on PATH): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// journalDir returns the local, gitignored journal directory for this repo.
func journalDir() (string, error) {
	gd, err := gitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(gd, "dun", "journal"), nil
}
