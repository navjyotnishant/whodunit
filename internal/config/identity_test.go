package config

import (
	"reflect"
	"testing"
)

func TestResolveIdentityLeavesUnmappedAddressesAlone(t *testing.T) {
	c := Config{Identities: map[string]string{"alt@example.com": "real@example.com"}}

	// The rule that matters: no mapping means the address is its own
	// identity. Silently folding an unknown address into a nearby one
	// would assert a person that nobody configured (NAV-21).
	if got := c.ResolveIdentity("someone@else.com"); got != "someone@else.com" {
		t.Errorf("unmapped address changed: %q", got)
	}
	if got := c.ResolveIdentity("alt@example.com"); got != "real@example.com" {
		t.Errorf("mapped address not resolved: %q", got)
	}
	if got := c.ResolveIdentity(""); got != "" {
		t.Errorf("empty address must stay empty: %q", got)
	}
}

func TestResolveIdentityWithNoMapConfigured(t *testing.T) {
	// The overwhelmingly common case: nobody has configured anything, and
	// every address must survive untouched.
	var c Config
	if got := c.ResolveIdentity("a@b.com"); got != "a@b.com" {
		t.Errorf("address changed with no map: %q", got)
	}
}

func TestResolveIdentityFollowsChains(t *testing.T) {
	// a -> b -> c so that retargeting an identity is one edit rather than
	// one edit per alias pointing at it.
	c := Config{Identities: map[string]string{
		"a@x.com": "b@x.com",
		"b@x.com": "c@x.com",
	}}
	if got := c.ResolveIdentity("a@x.com"); got != "c@x.com" {
		t.Errorf("chain not followed: %q", got)
	}
}

func TestResolveIdentitySurvivesACycle(t *testing.T) {
	// A hand-edited config can contain a loop. It must degrade to the
	// un-aliased answer, because this runs on the commit path and a hang
	// there costs someone their commit.
	c := Config{Identities: map[string]string{
		"a@x.com": "b@x.com",
		"b@x.com": "a@x.com",
	}}
	if got := c.ResolveIdentity("a@x.com"); got == "" {
		t.Fatal("cycle produced an empty identity")
	}
	if got := c.ResolveIdentity("c@x.com"); got != "c@x.com" {
		t.Errorf("unrelated address affected by a cycle: %q", got)
	}
}

func TestResolveIdentityIgnoresAnEmptyTarget(t *testing.T) {
	// An entry mapping to "" would erase an identity. Treat it as absent.
	c := Config{Identities: map[string]string{"a@x.com": ""}}
	if got := c.ResolveIdentity("a@x.com"); got != "a@x.com" {
		t.Errorf("empty target erased the identity: %q", got)
	}
}

func TestCanonicalIdentitiesExcludesAliases(t *testing.T) {
	c := Config{Identities: map[string]string{
		"alt@x.com": "real@x.com",
		"old@x.com": "real@x.com",
		"a@y.com":   "b@y.com",
	}}
	want := []string{"b@y.com", "real@x.com"}
	if got := c.CanonicalIdentities(); !reflect.DeepEqual(got, want) {
		t.Errorf("canonical identities = %v, want %v", got, want)
	}
	var empty Config
	if got := empty.CanonicalIdentities(); got != nil {
		t.Errorf("no map should yield nil, got %v", got)
	}
}

func TestIdentitiesRoundTripThroughConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHODUNIT_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c.Identities = map[string]string{"alt@x.com": "real@x.com"}
	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ResolveIdentity("alt@x.com") != "real@x.com" {
		t.Errorf("identities did not survive a round trip: %v", got.Identities)
	}
}
