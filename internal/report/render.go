package report

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// methodOrder is lowest-to-highest confidence, used everywhere method mix
// is displayed so the reader sees the same ordering every time.
var methodOrder = []spec.Method{
	spec.MethodUndetermined, spec.MethodDeclared, spec.MethodInferred,
	spec.MethodObserved, spec.MethodIntersected,
}

var purposeOrder = []purpose.Purpose{
	purpose.Feature, purpose.Fix, purpose.Test, purpose.Refactor, purpose.Docs,
	purpose.Config, purpose.Chore, purpose.Migration, purpose.Dependency, purpose.Other,
}

// Render writes a self-contained HTML report to w, using the default
// template. Kept for callers that do not choose one.
func Render(w *strings.Builder, stats Stats) {
	RenderTemplate(w, stats, Activity{}, TemplateExec)
}

// RenderTemplate writes a self-contained HTML report for one preset.
//
// Two inputs rather than one: stats comes from git history and is always
// available, act comes from the journal and may legitimately be empty. A
// single merged type would lose that distinction, and with it the ability
// to say "nothing was recorded" instead of showing zeros (NAV-21).
func RenderTemplate(w *strings.Builder, stats Stats, act Activity, tmpl Template) {
	w.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	w.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	w.WriteString("<title>Whodunit report</title>")
	w.WriteString(styleBlock)
	w.WriteString("</head><body>")

	fmt.Fprintf(w, "<h1>Whodunit report</h1>")
	fmt.Fprintf(w, `<p class="muted">%s &middot; %d commits examined</p>`,
		html.EscapeString(tmpl.Describe()), stats.TotalCommits)

	switch tmpl {
	case TemplateAdoption:
		renderAdoption(w, stats, act)
	case TemplateDetail:
		renderDetail(w, stats, act)
	default:
		renderExec(w, stats, act)
	}

	w.WriteString(`<p class="muted">Velocity and revert-rate deltas are not shown: ` +
		`they require a pre-adoption baseline (NAV-14) that has not been captured ` +
		`for this repo yet. A bare velocity number without that baseline would ` +
		`overstate confidence this report does not have.</p>`)
	w.WriteString("</body></html>")
}

func renderStatTile(w *strings.Builder, label, value, note string) {
	fmt.Fprintf(w, `<div class="tile"><div class="tile-value">%s</div><div class="tile-label">%s</div><div class="tile-note">%s</div></div>`,
		html.EscapeString(value), html.EscapeString(label), html.EscapeString(note))
}

// methodColor gives each method a fixed color so the same method reads the
// same way across the bar chart, the table, and (implicitly) any future
// panel — confidence level is legible at a glance, not just by label.
func methodColor(m spec.Method) string {
	switch m {
	case spec.MethodIntersected:
		return "#16a34a" // strongest evidence: green
	case spec.MethodObserved:
		return "#65a30d"
	case spec.MethodInferred:
		return "#ca8a04"
	case spec.MethodDeclared:
		return "#ea580c"
	default: // undetermined
		return "#71717a" // no evidence either way: neutral gray, never red —
		// undetermined is not a failure state (NAV-21), it's an honest unknown
	}
}

// renderMethodMixChart draws a horizontal stacked bar — one segment per
// method, width proportional to its share of covered commits. Inline SVG,
// no chart library, so the report stays a single dependency-free file.
func renderMethodMixChart(w *strings.Builder, stats Stats) {
	w.WriteString("<h2>Method mix</h2>")
	if stats.Covered == 0 {
		w.WriteString(`<p class="muted">No covered commits yet.</p>`)
		return
	}

	const width = 680
	const height = 32
	w.WriteString(fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="100%%" height="%d" role="img" aria-label="method mix">`, width, height, height))
	x := 0.0
	for _, m := range methodOrder {
		n := stats.MethodCount[m]
		if n == 0 {
			continue
		}
		segWidth := float64(width) * float64(n) / float64(stats.Covered)
		fmt.Fprintf(w, `<rect x="%.1f" y="0" width="%.1f" height="%d" fill="%s"><title>%s: %d</title></rect>`,
			x, segWidth, height, methodColor(m), html.EscapeString(string(m)), n)
		x += segWidth
	}
	w.WriteString("</svg>")

	// The method column explains itself. These names are spec vocabulary
	// and cannot be renamed without breaking the trailer, the dashboards
	// and every commit already stamped — but a reader meeting them for the
	// first time should not have to look them up.
	w.WriteString(`<table><tr><th>method</th><th>count</th><th></th><th>what it means</th></tr>`)
	for _, m := range methodOrder {
		if n := stats.MethodCount[m]; n > 0 {
			fmt.Fprintf(w, `<tr><td><span class="swatch" style="background:%s"></span>%s</td><td>%d</td><td class="muted">%.0f%%</td><td class="muted">%s</td></tr>`,
				methodColor(m), html.EscapeString(string(m)), n,
				100*float64(n)/float64(stats.Covered), html.EscapeString(m.Explain()))
		}
	}
	w.WriteString("</table>")
}

// renderPurposeBreakdown shows what kind of work the examined commits were —
// without this, a raw coverage number can't distinguish "AI wrote the core
// feature logic" from "AI only touched tests and config" (NAV-42).
func renderPurposeBreakdown(w *strings.Builder, stats Stats) {
	w.WriteString("<h2>Purpose distribution</h2>")
	if stats.TotalCommits == 0 {
		w.WriteString(`<p class="muted">No commits yet.</p>`)
		return
	}
	w.WriteString(`<table><tr><th>purpose</th><th>count</th><th></th></tr>`)
	for _, p := range purposeOrder {
		if n := stats.PurposeCount[p]; n > 0 {
			fmt.Fprintf(w, `<tr><td>%s</td><td>%d</td><td class="muted">%.0f%%</td></tr>`,
				html.EscapeString(string(p)), n, 100*float64(n)/float64(stats.TotalCommits))
		}
	}
	w.WriteString("</table>")
}

// renderCommitTable is the drill-down: every examined commit, its method,
// and its classified purpose, newest first — the piece a rollup number
// can't give you, letting a reader audit the data behind the tiles above.
func renderCommitTable(w *strings.Builder, stats Stats) {
	w.WriteString("<h2>Commits</h2>")
	if len(stats.Commits) == 0 {
		w.WriteString(`<p class="muted">No commits yet.</p>`)
		return
	}

	commits := make([]Commit, len(stats.Commits))
	copy(commits, stats.Commits)
	sort.SliceStable(commits, func(i, j int) bool { return commits[i].Timestamp.After(commits[j].Timestamp) })

	w.WriteString(`<table><tr><th>date</th><th>sha</th><th>subject</th><th>method</th><th>purpose</th></tr>`)
	for _, c := range commits {
		method := "—"
		methodStyle := ""
		if c.Trailer != nil {
			method = string(c.Trailer.Method)
			methodStyle = fmt.Sprintf(` style="color:%s"`, methodColor(c.Trailer.Method))
		}
		dateStr := "—"
		if !c.Timestamp.IsZero() {
			dateStr = c.Timestamp.Format("2006-01-02")
		}
		fmt.Fprintf(w, `<tr><td class="muted">%s</td><td class="mono">%s</td><td>%s</td><td%s>%s</td><td>%s</td></tr>`,
			dateStr, html.EscapeString(shortSHA(c.SHA)), html.EscapeString(c.Subject),
			methodStyle, html.EscapeString(method), html.EscapeString(string(c.Purpose)))
	}
	w.WriteString("</table>")
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

const styleBlock = `<style>
:root { --bg:#fff; --fg:#1a1a1a; --muted:#666; --card:#f4f4f5; --border:#e4e4e7; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#18181b; --fg:#e4e4e7; --muted:#a1a1aa; --card:#27272a; --border:#3f3f46; }
}
body { background:var(--bg); color:var(--fg); font-family:-apple-system,system-ui,sans-serif; max-width:820px; margin:2rem auto; padding:0 1rem; }
.muted { color:var(--muted); font-size:0.9rem; }
.mono { font-family:ui-monospace,monospace; font-size:0.85rem; }
:root { --accent:#2563eb; }
@media (prefers-color-scheme: dark) { :root { --accent:#60a5fa; } }
.chart { width:100%; height:140px; display:block; margin:0.5rem 0 0.25rem; }
.chart-axis { display:flex; justify-content:space-between; color:var(--muted); font-size:0.8rem; margin-bottom:1rem; }
.stack { display:flex; height:28px; border-radius:4px; overflow:hidden; margin:0.5rem 0; }
.stack-seg { height:100%; }
.stack-key { display:flex; flex-wrap:wrap; gap:1rem; font-size:0.85rem; color:var(--muted); margin-bottom:1rem; }
.key i { display:inline-block; width:10px; height:10px; border-radius:2px; margin-right:0.35rem; }
table.bars { width:100%; border-collapse:collapse; }
table.bars td { border:none; padding:0.2rem 0.4rem 0.2rem 0; vertical-align:middle; }
.bar-label { white-space:nowrap; font-size:0.9rem; }
.bar-count { text-align:right; font-variant-numeric:tabular-nums; color:var(--muted); width:4rem; }
.bar-cell { width:100%; }
.bar { height:14px; border-radius:3px; min-width:2px; }
.notice { border:1px solid var(--border); border-radius:8px; padding:1rem; margin:1.5rem 0; background:var(--card); }
.notice h2 { margin-top:0; }
.tile { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:1rem; margin:1rem 0; display:inline-block; min-width:180px; margin-right:0.75rem; }
.tile-value { font-size:2rem; font-weight:600; }
.tile-label { font-weight:600; margin-top:0.25rem; }
.tile-note { color:var(--muted); font-size:0.85rem; margin-top:0.25rem; max-width:220px; }
svg { border-radius:4px; margin:0.5rem 0; }
table { border-collapse:collapse; width:100%; margin-bottom:1.5rem; }
th, td { text-align:left; padding:0.4rem 0.6rem; border-bottom:1px solid var(--border); }
.swatch { display:inline-block; width:0.7em; height:0.7em; border-radius:2px; margin-right:0.4em; }
</style>`
