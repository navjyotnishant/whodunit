// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 wrapper over internal/linehash.

// Package linehash computes whodunit's per-line hashes for programs outside
// this module.
//
// API stability: v0. Names may still change before whodunit 1.0. The hash
// VALUES are a stored format and do not change: Of matches what dun writes
// today, and both are pinned by the compatibility suite.
//
// Two schemes exist side by side:
//
//   - Of: SHA-256 over the canonical absolute path and the trimmed line.
//     What dun stores and what AI-Attribution trailers are computed from.
//   - OfKeyed: HMAC-SHA256 with a versioned organisation key over the
//     repo-relative path and the trimmed line. Not guessable without the
//     key, and identical across clones. dun never computes it by default.
//
// Hashes from the two schemes never match each other.
package linehash

import "github.com/navjyotnishant/whodunit/internal/linehash"

// Key is a versioned secret for OfKeyed. Build it with NewKey.
type Key = linehash.Key

// MinKeyBytes is the shortest secret NewKey accepts.
const MinKeyBytes = linehash.MinKeyBytes

// ErrOutsideRepo is returned by RepoRelative for a path not under the root.
var ErrOutsideRepo = linehash.ErrOutsideRepo

// NewKey validates a secret and returns a Key carrying its version.
func NewKey(version int, secret []byte) (Key, error) { return linehash.NewKey(version, secret) }

// Of returns dun's unkeyed hash for one line in one file.
func Of(filePath, line string) uint64 { return linehash.Of(filePath, line) }

// OfText hashes every substantive line of text with Of.
func OfText(filePath, text string) []uint64 { return linehash.OfText(filePath, text) }

// OfKeyed returns the keyed hash for one line, given a repo-relative path.
// It panics on a zero Key.
func OfKeyed(k Key, relPath, line string) uint64 { return linehash.OfKeyed(k, relPath, line) }

// OfTextKeyed hashes every substantive line of text with OfKeyed.
func OfTextKeyed(k Key, relPath, text string) []uint64 {
	return linehash.OfTextKeyed(k, relPath, text)
}

// Canonical returns the spelling of a path both sides of a match must use.
func Canonical(p string) string { return linehash.Canonical(p) }

// RepoRelative returns path relative to the repository root, slash separated.
func RepoRelative(root, path string) (string, error) { return linehash.RepoRelative(root, path) }

// Substantive reports whether a line carries enough content to attribute.
func Substantive(line string) bool { return linehash.Substantive(line) }
