// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Public v0 access to the lines each commit added.

package attribution

import (
	"fmt"

	"github.com/navjyotnishant/whodunit/internal/attribution"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// CommittedLine is one distinct substantive line a commit added: its
// repo-relative path, the unkeyed hash dun syncs, and, from
// CommittedLinesKeyed only, the keyed hash.
type CommittedLine = attribution.CommittedLine

// RepoRoot returns the top level of the repository containing the current
// directory.
func RepoRoot() (string, error) { return attribution.RepoRoot() }

// CommittedLines returns the distinct substantive lines each commit added,
// keyed by full SHA, exactly as dun's opt-in committed-line sync reads them.
func CommittedLines(root string, shas []string) (map[string][]CommittedLine, error) {
	return attribution.CommittedLines(root, shas)
}

// CommittedLinesKeyed is CommittedLines plus KeyedHash: linehash.OfKeyed with
// key over each line's repo-relative path. For whodunit Pro; dun never calls
// it, so nothing dun stores or syncs changes.
func CommittedLinesKeyed(root string, shas []string, key linehash.Key) (map[string][]CommittedLine, error) {
	if key.IsZero() {
		return nil, fmt.Errorf("attribution: CommittedLinesKeyed needs a key from linehash.NewKey")
	}
	return attribution.CommittedLinesKeyed(root, shas, func(rel, content string) uint64 {
		return linehash.OfKeyed(key, rel, content)
	})
}
