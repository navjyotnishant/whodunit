// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Keyed line hashes over repo-relative paths (ADR 0008 in
// whodunit-pro). Opt-in only: nothing in dun calls these by default.

package linehash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// MinKeyBytes is the shortest secret NewKey accepts. 32 bytes matches the
// HMAC-SHA256 block of security the hash is meant to carry.
const MinKeyBytes = 32

// keyedDomain separates keyed line hashes from any other use of the same
// secret. It is part of the stored format: changing it changes every hash.
const keyedDomain = "whodunit/linehash/keyed/v1\x00"

// Key is a versioned secret for OfKeyed.
//
// Why keyed at all: Of hashes a path and a line with nothing secret, so a
// short line ("return nil", "x := 0") can be recovered by hashing guesses.
// Keyed with a per-organisation secret, a hash reveals nothing to anyone
// without the key. Version is not mixed into the hash (a new secret already
// changes every value); it is stored beside each hash so a reader knows
// which key produced it and rotation can run old and new side by side.
type Key struct {
	version int
	secret  []byte
}

// NewKey validates a secret and returns a Key carrying its version.
func NewKey(version int, secret []byte) (Key, error) {
	if version < 1 {
		return Key{}, fmt.Errorf("linehash: key version must be at least 1, got %d", version)
	}
	if len(secret) < MinKeyBytes {
		return Key{}, fmt.Errorf("linehash: key must be at least %d bytes, got %d", MinKeyBytes, len(secret))
	}
	s := make([]byte, len(secret))
	copy(s, secret)
	return Key{version: version, secret: s}, nil
}

// Version reports which key produced a hash, for storing beside it.
func (k Key) Version() int { return k.version }

// IsZero reports whether k was not built by NewKey.
func (k Key) IsZero() bool { return k.secret == nil }

// OfKeyed returns the keyed hash for one line in one file.
//
// relPath is the path relative to the repository root (see RepoRelative),
// so the same line in two clones of one repository hashes the same on every
// machine, unlike Of, which uses the absolute path. The path is normalised
// and the line trimmed exactly as Of does.
//
// It panics on a zero Key: hashing with no secret would silently produce
// values that look keyed and are not.
func OfKeyed(k Key, relPath, line string) uint64 {
	if k.IsZero() {
		panic("linehash: OfKeyed called with a zero Key; build one with NewKey")
	}
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(keyedDomain))
	mac.Write([]byte(normalizePath(relPath)))
	mac.Write([]byte{0})
	mac.Write([]byte(strings.TrimSpace(line)))
	return binary.BigEndian.Uint64(mac.Sum(nil)[:8])
}

// OfTextKeyed hashes every substantive line of text with OfKeyed, skipping
// the same lines OfText skips.
func OfTextKeyed(k Key, relPath, text string) []uint64 {
	var hashes []uint64
	for _, line := range strings.Split(text, "\n") {
		if !Substantive(line) {
			continue
		}
		hashes = append(hashes, OfKeyed(k, relPath, line))
	}
	return hashes
}

// ErrOutsideRepo is returned by RepoRelative for a path not under the root.
var ErrOutsideRepo = errors.New("linehash: path is outside the repository")

// RepoRelative returns path relative to the repository root, slash
// separated, after resolving symlinks in both, so /tmp against /private/tmp
// and symlinked checkouts agree.
//
// A path that no longer exists is routine (a transcript outlives the files
// it edited), so symlinks are resolved on its nearest existing parent and
// the missing tail is kept as written. Canonical cannot do this: it leaves a
// missing path unresolved, which would put it outside a resolved root.
func RepoRelative(root, path string) (string, error) {
	r, err := filepath.Rel(resolveExisting(root), resolveExisting(path))
	if err != nil {
		return "", fmt.Errorf("linehash: %w", err)
	}
	r = filepath.ToSlash(r)
	if r == "." || r == ".." || strings.HasPrefix(r, "../") || filepath.IsAbs(r) {
		return "", ErrOutsideRepo
	}
	return r, nil
}

// resolveExisting cleans p and resolves symlinks on its longest existing
// prefix, keeping any missing tail as written.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	var tail []string
	for cur := p; ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}
