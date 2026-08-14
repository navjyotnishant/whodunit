// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Owner-only file permissions on Windows, where chmod does nothing.

//go:build windows

// Windows has no Unix permission bits. os.Chmod can only toggle the
// read-only attribute, and os.Stat synthesises a mode — 0666 for any
// writable file, 0444 for a read-only one — from that attribute alone. It
// never reflects the actual access control list.
//
// So the Unix implementation is not merely imprecise here, it is vacuous:
// writing with 0600 grants nothing, and checking for 0600 afterwards reads
// back a number that was invented. Every file this package writes — the
// encrypted sync password and the key that decrypts it — was inheriting
// whatever the parent directory granted, typically Users:(RX) on a shared
// machine, and reporting itself as protected.
//
// What follows replaces the mode with the thing Windows actually enforces: a
// DACL naming exactly one trustee, the file's owner, with no inherited
// entries.
package secret

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// secureFile replaces path's access control list with one that names only
// the current user.
//
// PROTECTED_DACL_SECURITY_INFORMATION is what makes this stick: without it
// the entries inherited from the parent directory remain, and adding an
// owner-only entry alongside Users:(RX) protects nothing.
func secureFile(path string) error {
	owner, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolve the current user: %w", err)
	}

	access := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(owner),
		},
	}}

	acl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return fmt.Errorf("build an access control list: %w", err)
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

// checkFile reports the file as too permissive if anyone other than its
// owner, SYSTEM, or Administrators can reach it.
//
// Returns an empty string when the file is fine, and a short description of
// the problem otherwise — the same shape the Unix side returns, so callers
// need no platform awareness.
//
// Read through the descriptor's SDDL string rather than by walking the ACL:
// x/sys/windows exposes no entry-enumeration API, and SDDL is the documented
// textual form of exactly this structure. Each access-allowed entry appears
// as "(A;;perms;;;trustee)", so the trustees are the last field of each.
func checkFile(path string) string {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		// Unreadable security info is not evidence of a problem, and
		// reporting one would make `dun verify` fail for a reason nobody
		// could act on.
		return ""
	}

	owner, _, err := sd.Owner()
	if err != nil {
		return ""
	}

	for _, trustee := range allowedTrustees(sd.String()) {
		if isOwner(trustee, owner) || allowedByDefault(trustee) {
			continue
		}
		return fmt.Sprintf("readable by %s", trustee)
	}
	return ""
}

// isOwner reports whether an SDDL trustee names the file's owner.
//
// Not a string comparison against owner.String(). SDDL renders well-known
// accounts as two-letter aliases — the built-in Administrator appears as
// "LA", not as S-1-5-21-…-500 — so comparing rendered forms reported a
// correctly owner-only file as "readable by LA", which is the owner.
//
// Converting the trustee back to a SID compares the two as identities
// rather than as spellings.
func isOwner(trustee string, owner *windows.SID) bool {
	if owner == nil {
		return false
	}
	if trustee == owner.String() {
		return true
	}
	// An alias resolves through the same parser Windows uses for SDDL.
	sid, err := windows.StringToSid(trustee)
	if err != nil {
		return false
	}
	return sid.Equals(owner)
}

// allowedTrustees pulls the trustee of every access-allowed ACE out of an
// SDDL string.
//
// An ACE is "(type;flags;rights;object;inherit;trustee)". Only type "A"
// (access-allowed) grants anything; deny entries and audit entries cannot
// widen access.
func allowedTrustees(sddl string) []string {
	var out []string
	for _, ace := range strings.Split(sddl, "(") {
		end := strings.Index(ace, ")")
		if end < 0 {
			continue
		}
		fields := strings.Split(ace[:end], ";")
		if len(fields) != 6 || fields[0] != "A" {
			continue
		}
		if t := fields[5]; t != "" {
			out = append(out, t)
		}
	}
	return out
}

// allowedByDefault is the set of trustees whose access is not a weakening.
//
// SYSTEM and the Administrators group can read any file on the machine
// whatever this ACL says, so flagging them would produce a warning no action
// could clear. Both appear in SDDL as two-letter aliases.
func allowedByDefault(trustee string) bool {
	switch trustee {
	case "SY", // Local System
		"BA", // Builtin Administrators
		"OW": // Owner Rights
		return true
	}
	// Deliberately not "LA", the built-in Administrator account. When it is
	// the file's owner — as on a CI runner — isOwner already accepts it;
	// listing it here would also accept it on a machine where it is somebody
	// else, which is exactly the grant worth reporting.
	// A fully-written SID for the same accounts.
	for _, wk := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid,
		windows.WinBuiltinAdministratorsSid,
	} {
		if known, err := windows.CreateWellKnownSid(wk); err == nil &&
			known.String() == trustee {
			return true
		}
	}
	return false
}

func currentUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
