// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Turns an agent's session id into the opaque token the
// trailer carries.

package spec

import (
	"crypto/sha256"
	"encoding/hex"
)

// SessionTokenLength is how many hex characters of the digest the trailer
// carries. Sixteen is 64 bits: far beyond collision range for the number of
// sessions any repository will ever have, and short enough that the trailer
// stays readable.
const SessionTokenLength = 16

// SessionToken derives the trailer's session value from an agent's own
// session id.
//
// The raw id must not ship in a commit. On Claude Code it is also the
// transcript filename:
//
//	session=57113870-ecd7-4b29-8957-18ec3e564d3b
//	→ ~/.claude/projects/<slug>/57113870-….jsonl
//
// so a commit message would permanently record a pointer into a file
// holding every prompt of that session. Not a secret in itself, but a join
// key into data this project does not control and never meant to expose
// (NAV-7).
//
// Hashing with the repo id keeps the one property the trailer needs —
// commits from one working period share a value — while removing the two
// it should not have. The token names no file, and it is repo-scoped, so
// the same session in two repositories yields different tokens and cannot
// be correlated across them by anyone reading commit messages.
//
// Stable across rebase and clone, since repoID is the root commit SHA.
//
// The journal keeps the raw id locally: it is the deduplication key for
// ingest, and it is what lets someone find their own transcript on their
// own machine. That is a feature there and a leak in a pushed commit.
func SessionToken(repoID, session string) string {
	if session == "" {
		return ""
	}
	// repoID is included even when empty so the domain separator is always
	// present, keeping the derivation single-valued.
	sum := sha256.Sum256([]byte(repoID + "\x00" + session))
	return hex.EncodeToString(sum[:])[:SessionTokenLength]
}
