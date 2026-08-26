// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: Resolving a contributor onto the team they belong to.

package config

import "sort"

// Unassigned is the team a contributor with no mapping belongs to.
//
// A name rather than an empty string, because it has to survive being put
// in a dropdown and read by someone. "(unassigned)" says a person exists
// and their team is not recorded; an empty option says nothing and looks
// like a rendering bug.
//
// The parentheses keep it from colliding with a real team called
// "unassigned", and sort it beside the others rather than hiding it.
const Unassigned = "(unassigned)"

// ResolveTeam returns the team a contributor belongs to.
//
// Config is consulted first and wins outright. devlakeTeam is what
// DevLake's teams/team_users tables say, passed in rather than queried
// here so this package stays free of a database: the caller has the
// connection, and a resolver that opens one cannot be tested without one.
//
// The precedence is deliberate. DevLake's tables are populated by a
// connector nobody here controls, and a value someone typed on purpose
// should not be overruled by one that arrived on a sync. Verified on this
// install 2026-08-24: both tables are empty, so config is not merely
// preferred, it is the only source there is.
//
// A contributor in neither source is Unassigned rather than dropped. This
// is the whole reason the function returns a value instead of an
// ("", bool): every path produces a team, so no caller can accidentally
// filter a person out of existence by forgetting to handle the second
// return (NAV-21).
func (c Config) ResolveTeam(contributor, devlakeTeam string) string {
	if contributor == "" {
		return Unassigned
	}
	// Resolved first, so someone's second machine lands in the same team
	// as their first. Without this a GitHub noreply address is a separate
	// contributor with no team, which is exactly the split Identities
	// exists to close.
	canonical := c.ResolveIdentity(contributor)

	for team, members := range c.Teams {
		for _, m := range members {
			if m == contributor || c.ResolveIdentity(m) == canonical {
				return team
			}
		}
	}
	if devlakeTeam != "" {
		return devlakeTeam
	}
	return Unassigned
}

// TeamNames lists every team in config, sorted, plus Unassigned.
//
// Unassigned is always present, even when every configured contributor has
// a team. Someone will commit from an address nobody has mapped yet, and a
// dropdown that only lists teams that currently have members hides them the
// moment they appear — the option has to exist before the person does.
func (c Config) TeamNames() []string {
	names := make([]string, 0, len(c.Teams)+1)
	for team := range c.Teams {
		names = append(names, team)
	}
	sort.Strings(names)
	return append(names, Unassigned)
}

// TeamMembers lists the contributors configured for one team, sorted.
//
// Returns the addresses as written in config, not resolved: this reports
// what the configuration says, and a caller comparing it against observed
// contributors wants to see the mapping it will actually apply.
func (c Config) TeamMembers(team string) []string {
	members := append([]string(nil), c.Teams[team]...)
	sort.Strings(members)
	return members
}
