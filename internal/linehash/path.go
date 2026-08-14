// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: The one spelling of a path that both sides of a match use.

package linehash

import (
	"path/filepath"
	"strings"
)

// Canonical returns the spelling of a path that both sides of an
// attribution match must agree on.
//
// The path is part of every line hash, so the staged half and the recorded
// half have to produce byte-identical strings or nothing ever matches — and
// a failed match is not an error, it is a commit quietly stamped observed
// instead of intersected, with nothing to say why.
//
// This lived in four places and did three different things. resolveDiffPath
// and the claudecode adapter called EvalSymlinks; agy called Clean first and
// then EvalSymlinks; codex did neither and hashed whatever the transcript
// happened to contain. Any two of those disagree whenever EvalSymlinks fails
// — a deleted file, which is routine, since a transcript outlives the files
// it edited — and they disagree silently.
//
// One function, called by all four, is the only way that invariant holds.
// The alternative is a test policing four copies, which catches drift after
// someone has already written it.
//
// Cleaned before resolving so a path carrying "./" or a trailing separator
// normalises even when the file is gone. Separators folded to forward
// slashes last, because filepath.Clean produces the host's separator and
// the two halves of a match may be produced on different ones.
func Canonical(p string) string {
	if p == "" {
		return p
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return strings.ReplaceAll(p, `\`, "/")
}
