// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Hashes of the lines each commit added, for opt-in sync
// (ADR 0008 in whodunit-pro).

package attribution

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// CommittedLine is one distinct substantive line a commit added.
type CommittedLine struct {
	// Path is relative to the repository root, slash separated, as git
	// prints it.
	Path string

	// Hash is linehash.Of over the canonical absolute path on this machine,
	// the same value the commit hook computed for the staged line, so it
	// joins with the agent line hashes sync already publishes.
	Hash uint64
}

// commitMarker opens each commit in the combined log output. A NUL cannot
// start a diff line, so it cannot be confused with content.
const commitMarker = "\x00commit "

// CommittedLines returns the distinct substantive lines each commit added,
// keyed by full SHA.
//
// One git process for every commit, not one per commit: sync may cover
// hundreds. Each commit is diffed against its first parent, so a merge
// contributes the lines it brought in from the merged branch, and a root
// commit contributes everything it added. Prefixes are forced so a user's
// diff.noprefix setting cannot hide the file names, and external diff
// drivers and textconv are off so only real content is hashed.
//
// Lines are deduplicated per commit by hash, which already includes the
// path: a line repeated within one file counts once, as in Intersect.
func CommittedLines(root string, shas []string) (map[string][]CommittedLine, error) {
	out := make(map[string][]CommittedLine, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	cmd := exec.Command("git", "-C", root, "log", "--stdin", "--no-walk=unsorted",
		"--format=%x00commit %H", "--patch", "-m", "--first-parent", "--unified=0",
		"--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/")
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("attribution: commit lines: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	parseCommitLines(string(raw), linehash.Canonical(root), out)
	return out, nil
}

// parseCommitLines splits combined log output into one diff per commit and
// walks each.
func parseCommitLines(raw, root string, out map[string][]CommittedLine) {
	for _, chunk := range strings.Split(raw, commitMarker) {
		nl := strings.IndexByte(chunk, '\n')
		if nl < 0 {
			continue
		}
		sha := strings.TrimSpace(chunk[:nl])
		if sha == "" {
			continue
		}
		seen := map[uint64]bool{}
		lines := out[sha]
		walkAddedLines(chunk[nl+1:], root, func(rel, abs, content string) {
			h := linehash.Of(abs, content)
			if seen[h] {
				return
			}
			seen[h] = true
			lines = append(lines, CommittedLine{Path: rel, Hash: h})
		})
		out[sha] = lines
	}
}
