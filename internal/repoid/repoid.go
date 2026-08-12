// Package repoid derives a stable identifier for a git repository.
//
// The identifier is the repository's root commit SHA. That choice matters:
//
//   - A filesystem path breaks the moment the same repo is cloned to another
//     machine or directory, and records local paths in a shared store.
//   - A remote URL leaks the org and repo name, which NAV-25 forbids
//     recording at all.
//   - The root commit SHA is identical for everyone with the same history,
//     survives clones, worktrees, and renames, and reveals nothing on its
//     own.
package repoid

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ForCurrentRepo returns the identifier for the repository containing the
// current working directory.
func ForCurrentRepo() (string, error) {
	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolve repo root commit (unborn or not a git repo?): %w", err)
	}

	var roots []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			roots = append(roots, line)
		}
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("repository has no commits yet, so it has no stable identifier")
	}

	// A repository can have several root commits — histories merged with
	// --allow-unrelated-histories, or a subtree merge. git lists them in
	// traversal order, which is not stable across clones, so sort and take
	// the lowest to get the same answer everywhere.
	sort.Strings(roots)
	return roots[0], nil
}
