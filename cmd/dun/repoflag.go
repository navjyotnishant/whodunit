// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Resolves the --repo flag to a repo id, by path or by id.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
)

// resolveRepo turns the value of --repo into a repo id.
//
// Empty means the current directory, which keeps every existing invocation
// behaving exactly as before.
//
// The flag takes a path or a repo id. A path is the ergonomic default; an
// id is what actually scopes the journal, and `dun repos list` prints ids
// for repositories that may since have moved — so a registered repo stays
// reachable by id even when its path is stale.
//
// The second return is a human label for the repository, used when a
// command has to say out loud what it is about to act on.
func resolveRepo(flag string) (repoID string, label string, err error) {
	if flag == "" {
		id, err := currentRepoID()
		if err != nil {
			return "", "", err
		}
		return id, "this repository", nil
	}

	// An id before a path: ids are 40-char hex and cannot collide with a
	// real directory name in practice, and checking the registry first
	// means a moved repo resolves without touching the filesystem.
	if looksLikeRepoID(flag) {
		entries, lerr := registry.List()
		if lerr == nil {
			for _, e := range entries {
				if e.RepoID == flag {
					return e.RepoID, e.Path, nil
				}
			}
		}
		// An id that is not registered is still a usable scope: the
		// journal may hold rows for a repo that was removed from the
		// registry, and refusing to show them would strand the data.
		return flag, flag[:min(12, len(flag))], nil
	}

	return resolveRepoPath(flag)
}

// resolveRepoPath turns a filesystem path into a repo id, distinguishing
// the ways it can fail. Criterion 3 of NAV-51: naming the problem beats
// printing nothing, since an empty journal and an unreadable one look
// identical to the user otherwise.
func resolveRepoPath(path string) (string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("--repo %s: no such directory", path)
		}
		return "", "", fmt.Errorf("--repo %s: %w", path, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("--repo %s: not a directory", path)
	}

	// Ask git directly rather than through repoid.ForRepo, which folds
	// every failure into one message. The two cases need different
	// advice: one is the wrong directory, the other is a real repository
	// with nothing recorded yet.
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("--repo %s: not a git repository", path)
	}

	id, err := repoid.ForRepo(path)
	if err != nil {
		return "", "", fmt.Errorf("--repo %s: repository has no commits yet, so it has no id", path)
	}
	return id, path, nil
}

// looksLikeRepoID reports whether s has the shape of a root commit SHA.
// Deliberately strict: anything else is treated as a path, so a typo'd
// path reports a path error rather than being silently accepted as an id
// that matches nothing.
func looksLikeRepoID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
