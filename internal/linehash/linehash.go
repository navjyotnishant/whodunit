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
func Of(filePath, line string) uint64 {
	trimmed := strings.TrimSpace(line)
	sum := sha256.Sum256([]byte(filePath + "\x00" + trimmed))
	return binary.BigEndian.Uint64(sum[:8])
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
