// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: The SDDL parsing behind the Windows permission check.

//go:build windows

package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllowedTrusteesReadsAccessAllowedEntries(t *testing.T) {
	// SDDL is the textual form of a security descriptor. An ACE is
	// "(type;flags;rights;object-guid;inherit-guid;trustee)", and only type
	// "A" grants access — a deny entry cannot widen it, and treating one as
	// a grant would report a locked-down file as readable by whoever it
	// denies.
	cases := []struct {
		name string
		sddl string
		want []string
	}{
		{
			"owner only",
			"O:S-1-5-21-1-2-3-1001G:DUD:P(A;;FA;;;S-1-5-21-1-2-3-1001)",
			[]string{"S-1-5-21-1-2-3-1001"},
		},
		{
			"inherited entries let Users read",
			"O:S-1-5-21-1-2-3-1001G:DUD:AI(A;ID;FA;;;SY)(A;ID;FA;;;BA)(A;ID;0x1200a9;;;BU)",
			[]string{"SY", "BA", "BU"},
		},
		{
			"a deny entry is not a grant",
			"O:S-1-5-21-1-2-3-1001G:DUD:P(D;;FA;;;BG)(A;;FA;;;S-1-5-21-1-2-3-1001)",
			[]string{"S-1-5-21-1-2-3-1001"},
		},
		{
			"no dacl at all",
			"O:S-1-5-21-1-2-3-1001G:DUD:P",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := allowedTrustees(c.sddl)
			if len(got) != len(c.want) {
				t.Fatalf("allowedTrustees = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("trustee %d = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestAllowedByDefaultCoversTheUnavoidableAccounts(t *testing.T) {
	// SYSTEM and Administrators can read any file on the machine whatever
	// this ACL says, so reporting them would be a warning nobody could
	// clear. Everything else has to be reported.
	for _, trustee := range []string{"SY", "BA"} {
		if !allowedByDefault(trustee) {
			t.Errorf("%q should be allowed by default", trustee)
		}
	}
	for _, trustee := range []string{"BU", "WD", "AU", "S-1-5-21-9-9-9-1001"} {
		if allowedByDefault(trustee) {
			t.Errorf("%q must not be treated as harmless", trustee)
		}
	}
}

func TestSecureFileLeavesOnlyTheOwner(t *testing.T) {
	// The end-to-end property: after securing a file, the check passes —
	// and it is the ACL that makes it pass, since the mode bits Windows
	// reports are fabricated and would say 0666 either way.
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.key")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := secureFile(path); err != nil {
		t.Fatalf("secureFile: %v", err)
	}
	if problem := checkFile(path); problem != "" {
		t.Errorf("a freshly secured file reports %q", problem)
	}
}
