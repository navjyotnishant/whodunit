// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Skip Unix permission assertions where the mode bits are fiction.

// Package testmode holds the one thing several packages' tests need to agree
// on: whether this platform has Unix permission bits worth asserting.
package testmode

import (
	"runtime"
	"testing"
)

// SkipIfNoPermissionBits skips the calling test on platforms where a file
// mode does not describe what the operating system enforces.
//
// Windows has no Unix permission bits. os.Chmod can only toggle the
// read-only attribute, and os.Stat synthesises the mode it returns — 0666
// for any writable file, 0444 for a read-only one — from that attribute
// alone. A test asserting 0600 there is comparing against a number the
// runtime invented, so it can only ever fail, and making it pass would mean
// weakening the assertion everywhere it is real.
//
// The files this guards are not secrets: the journal, the hook log, the
// registry. They hold which files were edited and when, which is private but
// not a credential, and on Windows they inherit the user profile directory's
// access control list — owner plus SYSTEM and Administrators — which is the
// same protection the rest of a user's profile gets.
//
// The sync password is the exception and does not use this. internal/secret
// sets an explicit owner-only ACL on Windows and verifies it, because a
// credential inheriting whatever the parent directory grants is the case
// NAV-80 exists to prevent.
func SkipIfNoPermissionBits(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows has no unix permission bits; os.Stat's mode is synthesised")
	}
}
