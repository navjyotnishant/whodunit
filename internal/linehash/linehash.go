// Package linehash computes the unit both sides of attribution matching
// speak: one hash per line of code, scoped to the file it belongs to.
//
// Why lines rather than whole tool outputs (NAV-52): the journal used to
// hash what an agent's tool call produced — for a Write, the entire file.
// The staged diff yields contiguous blocks of added lines. Those only
// coincide when a file is created whole and committed untouched, so on
// this project's own history exactly 1 of 28 staged hunks ever matched.
//
// Hashing per line survives ordinary editing: the agent writes 200 lines,
// the developer keeps 150 and rewrites 50, and those 150 still match
// individually.
package linehash

import (
	"crypto/sha256"
	"encoding/binary"
	"path"
	"strings"
)

// Of returns the hash for one line in one file.
//
// The file path is part of the hash so that a common line — a closing
// brace, a blank line, `import "fmt"` — cannot match across unrelated
// files and manufacture attribution that never happened.
//
// Leading and trailing whitespace is trimmed: reindentation during review
// should not sever the link to the line an agent wrote, and indentation is
// not the part a reader would call authorship.
//
// The path is normalised — separators folded and the path cleaned — before
// hashing, because the two sides build it differently. The staged side joins the repo root to a diff target
// with filepath.Join, which yields `C:\repo\main.go` on Windows; the agent
// side takes the path an agent wrote into its transcript, which is
// `C:/repo/main.go` — Node and the agents themselves normalise that way.
// Hashing the raw string made those two disagree, so on Windows no staged
// line ever matched a recorded one and every commit fell back to observed
// or undetermined, with nothing to indicate why.
func Of(filePath, line string) uint64 {
	trimmed := strings.TrimSpace(line)
	sum := sha256.Sum256([]byte(normalizePath(filePath) + "\x00" + trimmed))
	return binary.BigEndian.Uint64(sum[:8])
}

// normalizePath makes a path hash the same however it was assembled.
//
// Separators only, and deliberately nothing that touches the filesystem:
// this runs once per line, and the commit hook has a two-second budget to
// hash thousands of them. Resolving symlinks or Windows' 8.3 short names
// here would mean a stat syscall per line of every diff.
//
// Those larger differences — /tmp against /private/tmp, RUNNER~1 against
// runneradmin — are collapsed once per file by the callers that know the
// repository root, before any line is hashed.
//
// Case is left alone even though Windows filesystems are usually
// case-insensitive: both sides of a match come from one machine within one
// commit, so they already agree on case, and folding it would merge
// genuinely distinct paths on the case-sensitive filesystems where most of
// this data is produced.
func normalizePath(p string) string {
	if p == "" {
		return p
	}
	// Separators first, then path.Clean — the slash-only Clean, not
	// filepath.Clean, so this stays pure string manipulation with no
	// syscall and no dependence on the host's separator.
	//
	// Cleaning here as well as in Canonical is deliberate. Callers are
	// expected to canonicalise once per file, but Of is exported and hashes
	// whatever it is given: without this, "/repo//main.go" and
	// "/repo/main.go" produced different hashes, so one caller passing an
	// uncleaned path would silently match nothing.
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// Set is a collection of line hashes for lookup.
type Set map[uint64]struct{}

// Add records a hash.
func (s Set) Add(h uint64) { s[h] = struct{}{} }

// Has reports whether the hash is present.
func (s Set) Has(h uint64) bool {
	_, ok := s[h]
	return ok
}

// OfText hashes every substantive line of a block of text.
//
// Blank lines and lines whose trimmed form is shorter than minLineLength
// are skipped. A blank line or a lone `}` carries no evidence of
// authorship and appears in nearly every file: counting them would inflate
// every ratio toward the share of boilerplate two files happen to share.
func OfText(filePath, text string) []uint64 {
	var hashes []uint64
	for _, line := range strings.Split(text, "\n") {
		if !Substantive(line) {
			continue
		}
		hashes = append(hashes, Of(filePath, line))
	}
	return hashes
}

// minLineLength is the trimmed length below which a line is treated as
// carrying no attribution evidence. Chosen to exclude `}`, `)`, `end`,
// `fi` and similar, while keeping short but real statements.
const minLineLength = 4

// Substantive reports whether a line carries enough content to be worth
// attributing.
func Substantive(line string) bool {
	return len(strings.TrimSpace(line)) >= minLineLength
}
