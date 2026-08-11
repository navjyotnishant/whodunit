// Package report computes coverage/method-mix/cost stats from git history
// and renders them into a single self-contained HTML file — no server, no
// CDN, no network call, so it works offline and on an intranet.
package report

import (
	"bufio"
	"fmt"
	"html"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Stats is the aggregate computed from git history for one report run.
type Stats struct {
	TotalCommits int
	Covered      int // commits with a valid trailer (any status)
	Assisted     int // commits with status=assisted (used in the trailer, not the commit)
	MethodCount  map[spec.Method]int
	MonthlySpend float64
}

// Coverage returns the fraction of commits carrying a valid trailer.
func (s Stats) Coverage() float64 {
	if s.TotalCommits == 0 {
		return 0
	}
	return float64(s.Covered) / float64(s.TotalCommits)
}

// Penetration returns the fraction of *covered* commits that are assisted.
// Denominator excludes undetermined commits deliberately (NAV-40): reporting
// penetration over all commits understates it, and without coverage alongside
// it overstates confidence — Coverage() must always be shown next to this.
func (s Stats) Penetration() float64 {
	if s.Covered == 0 {
		return 0
	}
	return float64(s.Assisted) / float64(s.Covered)
}

// Collect walks up to `limit` commits of git history and computes Stats.
func Collect(limit int) (Stats, error) {
	stats := Stats{MethodCount: map[spec.Method]int{}}

	out, err := exec.Command("git", "log", "-n", fmt.Sprint(limit), "--format=%B%x00").Output()
	if err != nil {
		// An empty/unborn repo (no commits yet) is a valid, empty report,
		// not a failure — anything else genuinely is.
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return withSpend(stats), nil
		}
		return Stats{}, fmt.Errorf("read git log: %w", err)
	}
	prefix := spec.TrailerKey + ":"

	for _, commitMsg := range strings.Split(string(out), "\x00") {
		commitMsg = strings.TrimSpace(commitMsg)
		if commitMsg == "" {
			continue
		}
		stats.TotalCommits++

		scanner := bufio.NewScanner(strings.NewReader(commitMsg))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			t, err := spec.Parse(strings.TrimSpace(line[len(prefix):]))
			if err != nil {
				continue
			}
			stats.Covered++
			stats.MethodCount[t.Method]++
			if t.Status == spec.StatusAssisted {
				stats.Assisted++
			}
			break
		}
	}

	return withSpend(stats), nil
}

func withSpend(stats Stats) Stats {
	cfg, err := config.Load()
	if err == nil {
		stats.MonthlySpend = cfg.MonthlySpend
	}
	return stats
}

// Render writes a self-contained HTML report for stats to w.
func Render(w *strings.Builder, stats Stats) {
	w.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	w.WriteString("<title>Whodunit report</title>")
	w.WriteString(styleBlock)
	w.WriteString("</head><body>")
	fmt.Fprintf(w, "<h1>Whodunit report</h1>")
	fmt.Fprintf(w, "<p class=\"muted\">%d commits examined</p>", stats.TotalCommits)

	renderStatTile(w, "Coverage", fmt.Sprintf("%.0f%%", stats.Coverage()*100),
		fmt.Sprintf("%d of %d commits carry a valid trailer", stats.Covered, stats.TotalCommits))
	renderStatTile(w, "Penetration", fmt.Sprintf("%.0f%%", stats.Penetration()*100),
		fmt.Sprintf("of covered commits, %d are AI-assisted (undetermined excluded from denominator)", stats.Assisted))

	if stats.MonthlySpend > 0 && stats.Assisted > 0 {
		perCommit := stats.MonthlySpend / float64(stats.Assisted)
		renderStatTile(w, "Cost per assisted commit", fmt.Sprintf("$%.2f", perCommit),
			"monthly subscription spend divided by assisted commits in this window — a rough proxy, not a precise unit cost")
	}

	w.WriteString("<h2>Method mix</h2><table><tr><th>method</th><th>count</th></tr>")
	for _, m := range []spec.Method{spec.MethodIntersected, spec.MethodObserved, spec.MethodInferred, spec.MethodDeclared, spec.MethodUndetermined} {
		if n := stats.MethodCount[m]; n > 0 {
			fmt.Fprintf(w, "<tr><td>%s</td><td>%d</td></tr>", html.EscapeString(string(m)), n)
		}
	}
	w.WriteString("</table>")

	w.WriteString("<p class=\"muted\">Velocity and revert-rate deltas are not shown: they require a pre-adoption baseline " +
		"(NAV-14) that has not been captured for this repo yet. A bare velocity number without that baseline " +
		"would overstate confidence this report does not have.</p>")

	w.WriteString("</body></html>")
}

func renderStatTile(w *strings.Builder, label, value, note string) {
	fmt.Fprintf(w, `<div class="tile"><div class="tile-value">%s</div><div class="tile-label">%s</div><div class="tile-note">%s</div></div>`,
		html.EscapeString(value), html.EscapeString(label), html.EscapeString(note))
}

const styleBlock = `<style>
:root { --bg:#fff; --fg:#1a1a1a; --muted:#666; --card:#f4f4f5; --border:#e4e4e7; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#18181b; --fg:#e4e4e7; --muted:#a1a1aa; --card:#27272a; --border:#3f3f46; }
}
body { background:var(--bg); color:var(--fg); font-family:-apple-system,system-ui,sans-serif; max-width:720px; margin:2rem auto; padding:0 1rem; }
.muted { color:var(--muted); font-size:0.9rem; }
.tile { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:1rem; margin:1rem 0; }
.tile-value { font-size:2rem; font-weight:600; }
.tile-label { font-weight:600; margin-top:0.25rem; }
.tile-note { color:var(--muted); font-size:0.85rem; margin-top:0.25rem; }
table { border-collapse:collapse; width:100%; }
th, td { text-align:left; padding:0.4rem 0.6rem; border-bottom:1px solid var(--border); }
</style>`
