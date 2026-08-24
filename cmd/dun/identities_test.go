package main

import "testing"

func TestIdentityHandleStripsANoreplyPrefix(t *testing.T) {
	// The case this whole command exists for: a GitHub noreply address and
	// a real one belonging to the same person share no display name, so
	// the handle is the only thing that links them.
	for _, tc := range []struct{ email, want string }{
		{"14622560+alice@users.noreply.github.com", "alice"},
		{"alice@example.com", "alice"},
		{"14622560@users.noreply.github.com", "14622560"},
		{"not-an-address", ""},
		{"", ""},
	} {
		if got := identityHandle(tc.email); got != tc.want {
			t.Errorf("identityHandle(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}

func TestIsToolNameExcludesBracketedNames(t *testing.T) {
	// Found in real data: two unrelated addresses both committed as
	// "[dyad]" because a tool authored on their behalf. Grouping on that
	// merged them, which is a false claim about who somebody is.
	for _, name := range []string{"[dyad]", "[bot]", "  [dependabot]  "} {
		if !isToolName(name) {
			t.Errorf("%q should read as a tool", name)
		}
	}
	for _, name := range []string{"Navjyot Nishant", "alice", "O'Brien"} {
		if isToolName(name) {
			t.Errorf("%q is a person, not a tool", name)
		}
	}
}
