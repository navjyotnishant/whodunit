// Author: Navjyot Nishant
// Created: 2026-08-24
// Last updated: 2026-08-24
// Description: Resolving a git committer email onto the person who owns it.

package config

import "sort"

// ResolveIdentity returns the canonical address for a git committer email.
//
// An address with no mapping is returned unchanged. That is the whole
// contract, and it is deliberately the boring one: absence of a mapping
// means "this address is its own identity", never "this person is unknown"
// and never "merge it with something that looks similar" (NAV-21).
//
// Chains resolve, so a -> b -> c yields c and an alias can be retargeted by
// editing one entry rather than every entry pointing at it. A cycle stops
// at its entry point rather than looping: a malformed config should degrade
// to the un-aliased answer, not hang a commit hook.
func (c Config) ResolveIdentity(email string) string {
	if email == "" || len(c.Identities) == 0 {
		return email
	}
	seen := map[string]bool{email: true}
	for {
		next, ok := c.Identities[email]
		if !ok || next == "" || seen[next] {
			return email
		}
		seen[next] = true
		email = next
	}
}

// CanonicalIdentities lists every canonical address the map resolves to,
// sorted, for reporting what a configuration will actually do.
//
// Addresses that are only ever keys are excluded: they resolve to something
// else and are not identities in their own right.
func (c Config) CanonicalIdentities() []string {
	if len(c.Identities) == 0 {
		return nil
	}
	set := map[string]bool{}
	for from := range c.Identities {
		set[c.ResolveIdentity(from)] = true
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
