// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The journal-derived half of a report — outcomes, tools,
// sessions, activity over time.

package report

import (
	"sort"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Activity is everything a report knows from the journal, as opposed to
// from commit trailers.
//
// Deliberately a separate type from Stats rather than more fields on it.
// The two have different failure modes: git history is always readable, and
// the journal may be empty because nothing was recorded, because it was
// purged, or because an agent's path is misconfigured. Keeping them apart
// is what lets a report say "no journal data" instead of rendering
// confident zeros (NAV-21).
type Activity struct {
	// Present is false when there is no journal at all — no database, or
	// no rows for this repository. A report must say so rather than show
	// zeros, which read as "no AI was used here".
	Present bool

	Events   int
	Sessions []journal.Session

	// Outcomes counts tool calls by what happened to them (NAV-54).
	Outcomes map[string]int

	// Tools counts calls per tool name, and LinesByTool the lines each
	// produced.
	Tools       map[string]int
	LinesByTool map[string]int

	// Agents counts events per agent, so a report shows the mix on a
	// machine using more than one.
	Agents map[string]int

	// Files counts events per file path, most-edited first when sorted.
	Files map[string]int

	// Daily is agent activity per day, oldest first — the time dimension
	// the report has never had.
	Daily []DayCount

	FirstSeen time.Time
	LastSeen  time.Time
}

// DayCount is one day's activity.
type DayCount struct {
	Day        time.Time
	Events     int
	LinesAdded int
	Accepted   int
	Decided    int
}

// AcceptanceRate returns accepted calls over decided ones, and whether
// there were any.
//
// Never a bare percentage: a rate over three decided calls is not the same
// claim as one over three hundred, and the caller needs the denominator to
// say so (NAV-54).
func (a Activity) AcceptanceRate() (rate float64, decided int, ok bool) {
	decided = a.Outcomes["accepted"] + a.Outcomes["rejected"] + a.Outcomes["failed"]
	if decided == 0 {
		return 0, 0, false
	}
	return float64(a.Outcomes["accepted"]) / float64(decided), decided, true
}

// TopTools returns tool names ordered by call count, most first.
func (a Activity) TopTools(n int) []string { return topN(a.Tools, n) }

// TopFiles returns file paths ordered by event count, most first.
func (a Activity) TopFiles(n int) []string { return topN(a.Files, n) }

func topN(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j] // stable output for equal counts
	})
	if n > 0 && len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// CollectActivity reads this repository's journal.
//
// A missing or unreadable journal is not an error: it returns Activity{}
// with Present false, and the report says there is no journal data. The
// alternative — failing the whole report — would make a local report
// unavailable to exactly the people who have not synced anything.
func CollectActivity(dataDir, repoID string) Activity {
	a := Activity{
		Outcomes:    map[string]int{},
		Tools:       map[string]int{},
		LinesByTool: map[string]int{},
		Agents:      map[string]int{},
		Files:       map[string]int{},
	}

	entries, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil || len(entries) == 0 {
		return a
	}
	a.Present = true
	a.Events = len(entries)

	byDay := map[string]*DayCount{}
	for _, e := range entries {
		if e.Outcome != "" {
			a.Outcomes[e.Outcome]++
		}
		if e.Tool != "" {
			a.Tools[e.Tool]++
			a.LinesByTool[e.Tool] += e.LinesAdded
		}
		if e.Agent != "" {
			a.Agents[e.Agent]++
		}
		if e.File != "" {
			a.Files[e.File]++
		}

		if a.FirstSeen.IsZero() || e.Timestamp.Before(a.FirstSeen) {
			a.FirstSeen = e.Timestamp
		}
		if e.Timestamp.After(a.LastSeen) {
			a.LastSeen = e.Timestamp
		}

		day := e.Timestamp.UTC().Truncate(24 * time.Hour)
		key := day.Format("2006-01-02")
		d, ok := byDay[key]
		if !ok {
			d = &DayCount{Day: day}
			byDay[key] = d
		}
		d.Events++
		d.LinesAdded += e.LinesAdded
		switch e.Outcome {
		case "accepted":
			d.Accepted++
			d.Decided++
		case "rejected", "failed":
			d.Decided++
		}
	}

	a.Daily = make([]DayCount, 0, len(byDay))
	for _, d := range byDay {
		a.Daily = append(a.Daily, *d)
	}
	sort.Slice(a.Daily, func(i, j int) bool { return a.Daily[i].Day.Before(a.Daily[j].Day) })

	if sessions, err := journal.ReadSessions(dataDir, repoID); err == nil {
		a.Sessions = sessions
	}
	return a
}
