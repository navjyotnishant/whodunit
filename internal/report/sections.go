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

	if stats.MonthlySpend <= 0 {
		out = append(out, Unavailable{
			What: "Cost per thousand lines",
			Why:  "no monthly spend is configured, so there is no numerator",
			// Names the file rather than a command: `dun config set` only
			// handles agent paths today, so advising it here would send
			// the reader to an error.
			Fix: "add a `monthly_spend` value to ~/.whodunit/config.json",
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

// costPerThousandLines is the unit the dashboards settled on.
//
// Per single line the figure is fractions of a cent, where two decimals
// round distinct months to the same value. Per assisted commit — what this
// report showed before — varies with how people split their commits, which
// is not a property of the agent.
func costPerThousandLines(stats Stats, act Activity) (float64, int, bool) {
	if stats.MonthlySpend <= 0 || !act.Present {
		return 0, 0, false
	}
	var lines int
	for _, n := range act.LinesByTool {
		lines += n
	}
	if lines == 0 {
		return 0, 0, false
	}
	return stats.MonthlySpend / float64(lines) * 1000, lines, true
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
