// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The three report presets and the sections they render.

package report

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

// Template names a report preset.
//
// Named presets rather than a template language. The same three questions
// are already answered by the DevLake dashboards in deploy/devlake, and a
// user-supplied template would make Stats a public API where every field
// rename breaks someone's file.
type Template string

const (
	// TemplateExec answers "is adoption growing, and is the work landing".
	TemplateExec Template = "exec"

	// TemplateAdoption answers "who and what is being used".
	TemplateAdoption Template = "adoption"

	// TemplateDetail answers "what exactly happened".
	TemplateDetail Template = "detail"
)

// Templates lists the valid names, in the order they are offered.
var Templates = []Template{TemplateExec, TemplateAdoption, TemplateDetail}

// ParseTemplate resolves a template name.
//
// An unknown name is an error naming the valid options rather than a silent
// fall back to the default: someone who typed `--template adoptoin` wants
// to know, not to receive a different report than they asked for.
func ParseTemplate(name string) (Template, error) {
	if name == "" {
		return TemplateExec, nil
	}
	for _, t := range Templates {
		if string(t) == name {
			return t, nil
		}
	}
	names := make([]string, len(Templates))
	for i, t := range Templates {
		names[i] = string(t)
	}
	return "", fmt.Errorf("unknown template %q (valid: %s)", name, strings.Join(names, ", "))
}

// Describe returns a one-line summary of what a template answers.
func (t Template) Describe() string {
	switch t {
	case TemplateAdoption:
		return "who and what is being used"
	case TemplateDetail:
		return "what exactly happened, per commit and per file"
	default:
		return "is adoption growing, and is the work landing"
	}
}

// renderExec is the default: the trend, the acceptance rate, the mix.
func renderExec(w *strings.Builder, stats Stats, act Activity) {
	renderStatTile(w, "Coverage", pct(stats.Coverage()),
		fmt.Sprintf("%d of %d commits carry a valid trailer", stats.Covered, stats.TotalCommits))
	renderStatTile(w, "Penetration", pct(stats.Penetration()),
		fmt.Sprintf("of covered commits, %d are AI-assisted (undetermined excluded)", stats.Assisted))

	if rate, decided, ok := act.AcceptanceRate(); ok {
		// Always with the denominator: a rate over three decided calls is
		// not the same claim as one over three hundred (NAV-54).
		renderStatTile(w, "Acceptance", pct(rate),
			fmt.Sprintf("%d of %d decided tool calls were accepted",
				act.Outcomes["accepted"], decided))
	}

	if stats.MonthlySpend > 0 && stats.Assisted > 0 {
		renderStatTile(w, "Cost per assisted commit",
			fmt.Sprintf("$%.2f", stats.MonthlySpend/float64(stats.Assisted)),
			"monthly spend divided by assisted commits — a rough proxy, not a unit cost")
	}

	renderActivityTrend(w, act)
	renderMethodMixChart(w, stats)
	renderPurposeBreakdown(w, stats)
}

// renderAdoption answers who and what: agents, tools, sessions.
func renderAdoption(w *strings.Builder, stats Stats, act Activity) {
	if !act.Present {
		renderNoJournal(w)
		return
	}

	renderStatTile(w, "Agent edits", fmt.Sprint(act.Events),
		fmt.Sprintf("recorded across %d session(s)", len(act.Sessions)))
	if rate, decided, ok := act.AcceptanceRate(); ok {
		renderStatTile(w, "Acceptance", pct(rate),
			fmt.Sprintf("%d of %d decided tool calls", act.Outcomes["accepted"], decided))
	}

	renderActivityTrend(w, act)
	renderOutcomes(w, act)
	renderTools(w, act)
	renderAgents(w, act)
	renderSessions(w, act)
}

// renderDetail is the audit view: every commit, the most-touched files.
func renderDetail(w *strings.Builder, stats Stats, act Activity) {
	renderStatTile(w, "Coverage", pct(stats.Coverage()),
		fmt.Sprintf("%d of %d commits carry a valid trailer", stats.Covered, stats.TotalCommits))
	renderMethodMixChart(w, stats)
	renderCommitTable(w, stats)
	if act.Present {
		renderFiles(w, act)
		renderOutcomes(w, act)
	} else {
		renderNoJournal(w)
	}
}

// renderNoJournal says the journal is empty, rather than rendering zeros.
//
// The distinction matters more than it looks. Someone who has never used an
// agent, someone whose journal was purged, and someone whose agent path is
// misconfigured (NAV-71) all produce the same empty journal — and a wall of
// confident zeros would tell all three of them "no AI was used here", which
// is exactly the claim NAV-21 exists to prevent.
func renderNoJournal(w *strings.Builder) {
	w.WriteString(`<div class="notice"><h2>No journal data</h2>`)
	w.WriteString(`<p>Nothing has been recorded for this repository yet, so the ` +
		`sections below cannot be shown. That is not the same as "no AI was used": ` +
		`it means nothing was observed.</p>`)
	w.WriteString(`<p class="muted">Commit with an agent-edited file, or run ` +
		`<code>dun config agents</code> to check that whodunit is looking in the ` +
		`right place for your agent's transcripts.</p></div>`)
}

func renderActivityTrend(w *strings.Builder, act Activity) {
	if len(act.Daily) < 2 {
		// One day is not a trend. Saying so beats drawing a line through a
		// single point, which implies a shape the data does not have.
		if act.Present {
			w.WriteString(`<h2>Activity over time</h2>`)
			w.WriteString(`<p class="muted">Not enough history yet — a trend needs ` +
				`activity on more than one day.</p>`)
		}
		return
	}

	events := make([]float64, len(act.Daily))
	labels := make([]string, len(act.Daily))
	total := 0
	for i, d := range act.Daily {
		events[i] = float64(d.Events)
		labels[i] = d.Day.Format("2 Jan")
		total += d.Events
	}

	w.WriteString(`<h2>Activity over time</h2>`)
	fmt.Fprintf(w, `<p class="muted">%d agent edits across %d days</p>`, total, len(act.Daily))
	w.WriteString(sparkline(events, labels, "var(--accent)"))
}

func renderOutcomes(w *strings.Builder, act Activity) {
	if len(act.Outcomes) == 0 {
		return
	}
	w.WriteString(`<h2>What happened to each tool call</h2>`)
	w.WriteString(stackedBar([]Segment{
		{Label: "accepted", Count: act.Outcomes["accepted"], Color: "#16a34a"},
		{Label: "rejected", Count: act.Outcomes["rejected"], Color: "#ea580c"},
		{Label: "failed", Count: act.Outcomes["failed"], Color: "#dc2626"},
		{Label: "unknown", Count: act.Outcomes["unknown"], Color: "#71717a"},
	}))
	if act.Outcomes["unknown"] > 0 {
		w.WriteString(`<p class="muted">"Unknown" is a call whose result was not ` +
			`recorded — a session still running, or an agent that reports no ` +
			`accept/reject signal. It is not counted as accepted.</p>`)
	}
}

func renderTools(w *strings.Builder, act Activity) {
	tools := act.TopTools(10)
	if len(tools) == 0 {
		return
	}
	max := 0
	for _, t := range tools {
		if act.Tools[t] > max {
			max = act.Tools[t]
		}
	}
	w.WriteString(`<h2>Tools</h2><table class="bars">`)
	for _, t := range tools {
		w.WriteString(barRow(t, act.Tools[t], max, "var(--accent)"))
	}
	w.WriteString(`</table>`)
}

func renderAgents(w *strings.Builder, act Activity) {
	if len(act.Agents) == 0 {
		return
	}
	names := make([]string, 0, len(act.Agents))
	for n := range act.Agents {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return act.Agents[names[i]] > act.Agents[names[j]] })

	max := act.Agents[names[0]]
	w.WriteString(`<h2>Agents</h2><table class="bars">`)
	for _, n := range names {
		w.WriteString(barRow(n, act.Agents[n], max, "#65a30d"))
	}
	w.WriteString(`</table>`)
}

func renderSessions(w *strings.Builder, act Activity) {
	if len(act.Sessions) == 0 {
		return
	}
	w.WriteString(`<h2>Sessions</h2><table><tr><th>session</th><th>agent</th>` +
		`<th>prompts</th><th>tool calls</th><th>tools</th></tr>`)
	for _, s := range act.Sessions {
		id := s.Session
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(w, `<tr><td><code>%s</code></td><td>%s</td><td>%d</td><td>%d</td><td>%d</td></tr>`,
			html.EscapeString(id), html.EscapeString(s.Agent),
			s.UserMessages, s.ToolCalls, s.DistinctTools)
	}
	w.WriteString(`</table>`)
	w.WriteString(`<p class="muted">Counts only — no message content is recorded.</p>`)
}

func renderFiles(w *strings.Builder, act Activity) {
	files := act.TopFiles(15)
	if len(files) == 0 {
		return
	}
	max := act.Files[files[0]]
	w.WriteString(`<h2>Most agent-edited files</h2><table class="bars">`)
	for _, f := range files {
		w.WriteString(barRow(shortPath(f), act.Files[f], max, "var(--accent)"))
	}
	w.WriteString(`</table>`)
}

// shortPath trims a path to its last two segments, which is enough to
// recognise a file without a column of identical directory prefixes.
func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }
