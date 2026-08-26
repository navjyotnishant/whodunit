// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: Team resolution: config wins, nobody vanishes.

package config

import (
	"reflect"
	"testing"
)

func TestConfigWinsOverDevLake(t *testing.T) {
	c := Config{Teams: map[string][]string{
		"platform": {"alice@example.com"},
	}}

	// DevLake says growth, config says platform. The acceptance criterion
	// this story was sized for.
	if got := c.ResolveTeam("alice@example.com", "growth"); got != "platform" {
		t.Errorf("got %q, want platform — a value someone typed on purpose "+
			"was overruled by one that arrived on a sync", got)
	}
}

func TestDevLakeIsUsedWhenConfigIsSilent(t *testing.T) {
	c := Config{Teams: map[string][]string{"platform": {"bob@example.com"}}}

	if got := c.ResolveTeam("alice@example.com", "growth"); got != "growth" {
		t.Errorf("got %q, want growth", got)
	}
}

// The failure this whole area exists to prevent: a person disappearing.
func TestAContributorInNeitherSourceIsUnassigned(t *testing.T) {
	c := Config{Teams: map[string][]string{"platform": {"bob@example.com"}}}

	if got := c.ResolveTeam("nobody@example.com", ""); got != Unassigned {
		t.Errorf("got %q, want %q — an unmapped contributor must stay "+
			"reachable, not vanish (NAV-21)", got, Unassigned)
	}
}

// Unassigned is offered even when nobody is currently unassigned.
//
// Someone will commit from an unmapped address, and a dropdown built from
// current membership hides them the moment they appear.
func TestUnassignedIsAlwaysOffered(t *testing.T) {
	c := Config{Teams: map[string][]string{
		"platform": {"alice@example.com"},
		"growth":   {"bob@example.com"},
	}}

	want := []string{"growth", "platform", Unassigned}
	if got := c.TeamNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A second machine must land in the same team as the first.
//
// One person with a GitHub noreply address on one machine and their real
// address on another is two contributors to git. Without resolving the
// alias, the second address has no team and the person is split across
// "platform" and "(unassigned)" — which reads as two people, one of whom
// is unaccounted for.
func TestAnAliasedAddressResolvesToItsTeam(t *testing.T) {
	c := Config{
		Teams:      map[string][]string{"platform": {"alice@example.com"}},
		Identities: map[string]string{"12345+alice@users.noreply.github.com": "alice@example.com"},
	}

	if got := c.ResolveTeam("12345+alice@users.noreply.github.com", ""); got != "platform" {
		t.Errorf("got %q, want platform — the alias was treated as a "+
			"different person with no team", got)
	}
}

// An empty config resolves everyone rather than erroring.
//
// The default state: no teams block, DevLake's tables empty. Panels have to
// render from this, not break on it.
func TestAnEmptyConfigStillResolves(t *testing.T) {
	var c Config

	if got := c.ResolveTeam("alice@example.com", ""); got != Unassigned {
		t.Errorf("got %q, want %q", got, Unassigned)
	}
	if got := c.TeamNames(); !reflect.DeepEqual(got, []string{Unassigned}) {
		t.Errorf("got %v, want just %q", got, Unassigned)
	}
}

func TestAnEmptyContributorIsUnassigned(t *testing.T) {
	c := Config{Teams: map[string][]string{"platform": {"alice@example.com"}}}

	// Not matched against a team member whose entry happens to be empty.
	if got := c.ResolveTeam("", "growth"); got != Unassigned {
		t.Errorf("got %q, want %q", got, Unassigned)
	}
}
