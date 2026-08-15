// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Sections the Grafana dashboards proved worth having, ported
// to the local report.

package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// renderSurvival separates "an agent worked on this" from "the agent's
// output is what shipped".
//
// The distinction the method names carry and nothing in the report drew
// out. `intersected` means the agent's exact lines were still in the diff at
// commit time; `observed` means it edited the file and the text had changed
// before committing. Both are real work, and the ratio between them is a
// more honest adoption signal than a single percentage — a team at 90%
// coverage where almost everything is `observed` is not doing what a team at
// 90% `intersected` is doing.
func renderSurvival(w *strings.Builder, stats Stats) {
	intersected := stats.MethodCount[spec.MethodIntersected]
	observed := stats.MethodCount[spec.MethodObserved]
	total := intersected + observed
	if total == 0 {
		return
	}

	w.WriteString(`<h2>Did the agent's work survive?</h2>`)
	w.WriteString(`<p class="muted">Of ` + fmt.Sprint(total) +
		` commits with line-level evidence, how many kept the agent's own lines.</p>`)

	w.WriteString(`<table class="bars">`)
	for _, row := range []struct {
		label, note string
		n           int
		color       string
	}{
		{"survived to the commit", "the agent's exact lines are in the diff",
			intersected, methodColor(spec.MethodIntersected)},
		{"edited before committing", "the agent touched the file, the text changed",
			observed, methodColor(spec.MethodObserved)},
	} {
		pctOf := float64(row.n) / float64(total) * 100
		fmt.Fprintf(w, `<tr><td class="bar-label">%s<br><span class="muted">%s</span></td>`+
			`<td class="bar-cell"><span class="bar" style="width:%.1f%%;background:%s"></span></td>`+
			`<td class="bar-count">%d <span class="muted">(%.0f%%)</span></td></tr>`,
			row.label, row.note, pctOf, row.color, row.n, pctOf)
	}
	w.WriteString(`</table>`)
}

// renderIntensity groups sessions by how much work the agent actually did.
//
// Adoption counts anyone who used an agent at all, which flattens the
// difference between a session that answered a question and one that wrote
// across a dozen files. On the machine this was built against, four sessions
// out of ninety-five carried 99% of the tool calls — a fact no adoption
// percentage can show.
func renderIntensity(w *strings.Builder, act Activity) {
	if len(act.Sessions) == 0 {
		return
	}

	type band struct {
		label, note string
		n, calls    int
	}
	bands := []band{
		{label: "agentic", note: "used MCP servers, or five or more distinct tools"},
		{label: "wrote code", note: "ten or more tool calls"},
		{label: "light edits", note: "fewer than ten tool calls"},
		{label: "conversation only", note: "no file was edited"},
	}

	for _, s := range act.Sessions {
		switch {
		case s.MCPCalls > 0 || s.DistinctTools >= 5:
			bands[0].n++
			bands[0].calls += s.ToolCalls
		case s.ToolCalls >= 10:
			bands[1].n++
			bands[1].calls += s.ToolCalls
		case s.ToolCalls > 0:
			bands[2].n++
			bands[2].calls += s.ToolCalls
		default:
			bands[3].n++
		}
	}

	var totalCalls int
	for _, b := range bands {
		totalCalls += b.calls
	}

	w.WriteString(`<h2>How deeply was the agent used?</h2>`)
	w.WriteString(`<p class="muted">Adoption counts anyone who used an agent. ` +
		`This counts what the agent did — a session that answered a question and one ` +
		`that wrote across a dozen files are not the same thing.</p>`)

	w.WriteString(`<table class="bars">`)
	for _, b := range bands {
		if b.n == 0 {
			continue
		}
		share := float64(b.n) / float64(len(act.Sessions)) * 100
		callShare := ""
		if totalCalls > 0 && b.calls > 0 {
			callShare = fmt.Sprintf(` · %.0f%% of all tool calls`,
				float64(b.calls)/float64(totalCalls)*100)
		}
		fmt.Fprintf(w, `<tr><td class="bar-label">%s<br><span class="muted">%s</span></td>`+
			`<td class="bar-cell"><span class="bar" style="width:%.1f%%"></span></td>`+
			`<td class="bar-count">%d <span class="muted">session(s)%s</span></td></tr>`,
			b.label, b.note, share, b.n, callShare)
	}
	w.WriteString(`</table>`)
}

// renderUnavailable states what could not be measured and what would fix it.
//
// A metric that silently does not appear is indistinguishable from one that
// was never built, and a zero in its place reads as a measurement. Naming
// the gap costs three lines and is the difference between a reader trusting
// the rest of the numbers and wondering what else is missing.
func renderUnavailable(w *strings.Builder, items []Unavailable) {
	if len(items) == 0 {
		return
	}
	w.WriteString(`<h2>Not measurable here</h2>`)
	w.WriteString(`<div class="notice">`)
	for _, it := range items {
		fmt.Fprintf(w, `<p><strong>%s</strong> — %s`, it.What, it.Why)
		if it.Fix != "" {
			fmt.Fprintf(w, `<br><span class="muted">%s</span>`, it.Fix)
		}
		w.WriteString(`</p>`)
	}
	w.WriteString(`</div>`)
}

// Unavailable is one metric the report cannot compute, and why.
type Unavailable struct {
	What string
	Why  string
	Fix  string
}

// unavailableFor works out which metrics are missing and what is needed.
//
// Computed from the data rather than listed statically, so a repository that
// gains a baseline stops being told it lacks one.
func unavailableFor(stats Stats, act Activity, hasBaseline bool) []Unavailable {
	var out []Unavailable

	if !hasBaseline {
		out = append(out, Unavailable{
			What: "Before-and-after comparison",
			Why: "no pre-adoption baseline exists for this repository, and that " +
				"window closed when the hooks were installed",
			Fix: "`dun init` captures one automatically now, so repositories " +
				"instrumented from here on will have it",
		})
	}

	if act.Present && len(TokensByModel(act.Sessions)) == 0 {
		out = append(out, Unavailable{
			What: "Token use",
			Why: "no session recorded token counts — Antigravity does not report " +
				"them at all, and an older journal predates their collection",
			Fix: "`dun ingest` re-reads the transcripts; Claude Code and Codex " +
				"both report usage on every turn",
		})
	}

	if act.Present {
		if _, decided, ok := act.AcceptanceRate(); !ok || decided < 10 {
			out = append(out, Unavailable{
				What: "Acceptance rate",
				Why: fmt.Sprintf("only %d tool call(s) have a recorded outcome — "+
					"a rate over that few moves on one call", decided),
				Fix: "this fills in as more work is recorded",
			})
		}
	}

	return out
}

// topFiles returns the most-edited files, for the detail template.
func topFiles(act Activity, n int) []struct {
	Name  string
	Count int
} {
	type kv = struct {
		Name  string
		Count int
	}
	out := make([]kv, 0, len(act.Files))
	for name, c := range act.Files {
		out = append(out, kv{name, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// renderCodeVolume charts lines written over time.
//
// The activity chart counts edits, which says how busy the agent was and
// nothing about how much code resulted. A day of one large refactor and a
// day of forty one-line fixes look identical there and are not the same
// day.
func renderCodeVolume(w *strings.Builder, act Activity) {
	if len(act.Daily) < 2 {
		return
	}

	added := make([]float64, len(act.Daily))
	labels := make([]string, len(act.Daily))
	var total int
	for i, d := range act.Daily {
		added[i] = float64(d.LinesAdded)
		labels[i] = d.Day.Format("2 Jan")
		total += d.LinesAdded
	}
	if total == 0 {
		return
	}

	w.WriteString(`<h2>Lines written over time</h2>`)
	fmt.Fprintf(w, `<p class="muted">%s lines across %d days — volume, not value</p>`,
		humanInt(total), len(act.Daily))
	w.WriteString(sparkline(added, labels, "#3b82f6"))
}

// renderTopFiles lists where the agent's work landed.
//
// Concentration is the signal. An agent spread evenly across a codebase is
// being used differently from one that has rewritten the same three files
// twenty times, and neither the commit count nor the line count separates
// them.
func renderTopFiles(w *strings.Builder, act Activity, n int) {
	files := topFiles(act, n)
	if len(files) == 0 {
		return
	}

	max := files[0].Count
	w.WriteString(`<h2>Where the work landed</h2>`)
	fmt.Fprintf(w, `<p class="muted">the %d most-edited files, of %d touched</p>`,
		len(files), len(act.Files))
	w.WriteString(`<table class="bars">`)
	for _, f := range files {
		fmt.Fprintf(w, `<tr><td class="bar-label mono">%s</td>`+
			`<td class="bar-cell"><span class="bar" style="width:%.1f%%"></span></td>`+
			`<td class="bar-count">%d</td></tr>`,
			shortPath(f.Name), float64(f.Count)/float64(max)*100, f.Count)
	}
	w.WriteString(`</table>`)
}

// renderAgentMix shows which agents did the work.
//
// Rendered only when more than one has been seen. A single-agent bar chart
// says "100% claude-code", which is a fact the reader already has from every
// other section.
func renderAgentMix(w *strings.Builder, act Activity) {
	if len(act.Agents) < 2 {
		return
	}

	type kv struct {
		name string
		n    int
	}
	rows := make([]kv, 0, len(act.Agents))
	total := 0
	for name, n := range act.Agents {
		rows = append(rows, kv{name, n})
		total += n
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })

	w.WriteString(`<h2>Which agent</h2>`)
	w.WriteString(`<table class="bars">`)
	for _, r := range rows {
		share := float64(r.n) / float64(total) * 100
		fmt.Fprintf(w, `<tr><td class="bar-label">%s</td>`+
			`<td class="bar-cell"><span class="bar" style="width:%.1f%%"></span></td>`+
			`<td class="bar-count">%d <span class="muted">(%.0f%%)</span></td></tr>`,
			r.name, share, r.n, share)
	}
	w.WriteString(`</table>`)
}

// renderCommitSize compares assisted commits against the rest.
//
// The comparison the executive summary is for, and the one most easily
// misread — so it says what it is not. A larger assisted commit is a fact
// about which work was given to an agent, not evidence that the agent makes
// commits larger.
func renderCommitSize(w *strings.Builder, stats Stats) {
	var aCount, aLines, oCount, oLines int
	for _, c := range stats.Commits {
		// Trailer is nil on any commit without a valid one, which is every
		// commit made before the hooks were installed. Dereferencing it
		// crashed the whole report on the first such commit.
		if c.Trailer != nil && c.Trailer.Status == spec.StatusAssisted {
			aCount++
			aLines += c.LinesAdded + c.LinesRemoved
			continue
		}
		oCount++
		oLines += c.LinesAdded + c.LinesRemoved
	}
	if aCount == 0 || oCount == 0 {
		return
	}

	aAvg := float64(aLines) / float64(aCount)
	oAvg := float64(oLines) / float64(oCount)
	max := aAvg
	if oAvg > max {
		max = oAvg
	}

	w.WriteString(`<h2>Commit size</h2>`)
	w.WriteString(`<p class="muted">average lines changed per commit. A larger ` +
		`assisted commit describes which work was handed to an agent, not what ` +
		`the agent does to commit size.</p>`)
	w.WriteString(`<table class="bars">`)
	for _, row := range []struct {
		label string
		avg   float64
		n     int
	}{
		{"AI-assisted", aAvg, aCount},
		{"the rest", oAvg, oCount},
	} {
		fmt.Fprintf(w, `<tr><td class="bar-label">%s</td>`+
			`<td class="bar-cell"><span class="bar" style="width:%.1f%%"></span></td>`+
			`<td class="bar-count">%.0f <span class="muted">lines · %d commit(s)</span></td></tr>`,
			row.label, row.avg/max*100, row.avg, row.n)
	}
	w.WriteString(`</table>`)
}

// humanInt groups thousands, because 152331 and 15233 are hard to tell
// apart at a glance and the difference matters.
func humanInt(n int) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
