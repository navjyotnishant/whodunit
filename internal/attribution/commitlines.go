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

	"github.com/navjyotnishant/whodunit/internal/gitexec"
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

	// KeyedHash is filled only by CommittedLinesKeyed, with the hasher its
	// caller passes (pkg/attribution passes linehash.OfKeyed for whodunit
	// Pro); zero otherwise. dun never asks for it, so nothing dun writes or
	// syncs changes.
	KeyedHash uint64
}

// gitBin is git resolved to an absolute path once, rather than a bare name
// looked up through PATH on every call (as in internal/repoid).
var gitBin = func() string {
	if p, err := exec.LookPath("git"); err == nil {
		return p
	}
	return "git"
}()

// RepoRoot returns the top level of the repository containing the current
// directory.
func RepoRoot() (string, error) {
	out, err := gitexec.Output(gitBin, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("attribution: find repository root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
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
	return committedLines(root, shas, nil)
}

// LineHasher computes a second hash of an added line from its repo-relative
// path and content. Kept as a function so keyed hashing stays out of
// internal/ (the OfKeyed guard test).
type LineHasher func(relPath, content string) uint64

// CommittedLinesKeyed is CommittedLines plus KeyedHash on every line, from
// hasher, computed while the line is in memory. The unkeyed Hash and the
// deduplication are exactly CommittedLines'.
func CommittedLinesKeyed(root string, shas []string, hasher LineHasher) (map[string][]CommittedLine, error) {
	if hasher == nil {
		return nil, fmt.Errorf("attribution: CommittedLinesKeyed needs a hasher")
	}
	return committedLines(root, shas, hasher)
}

func committedLines(root string, shas []string, hasher LineHasher) (map[string][]CommittedLine, error) {
	out := make(map[string][]CommittedLine, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	cmd, cancel := gitexec.Command(gitBin, "-C", root, "log", "--stdin", "--no-walk=unsorted",
		"--format=%x00commit %H", "--patch", "-m", "--first-parent", "--unified=0",
		"--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/")
	defer cancel()
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("attribution: commit lines: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	parseCommitLines(string(raw), linehash.Canonical(root), hasher, out)
	return out, nil
}

// parseCommitLines splits combined log output into one diff per commit and
// walks each.
func parseCommitLines(raw, root string, hasher LineHasher, out map[string][]CommittedLine) {
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
			cl := CommittedLine{Path: rel, Hash: h}
			if hasher != nil {
				cl.KeyedHash = hasher(rel, content)
			}
			lines = append(lines, cl)
		})
		out[sha] = lines
	}
}
