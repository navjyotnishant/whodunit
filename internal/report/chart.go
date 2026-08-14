// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Inline SVG charts for the local report.

package report

import (
	"fmt"
	"html"
	"strings"
)

// Charts are generated as inline SVG, with no library and no external
// request.
//
// The report's whole value is being one self-contained file: it opens
// offline, behind a firewall, and as an email attachment. A CDN link would
// phone out to a third party every time someone reads it, and a vendored
// charting library adds a couple of hundred kilobytes per report plus a
// dependency to keep patched. Neither is worth it for bars and a line.
//
// Colours come from CSS custom properties defined in the style block, so a
// chart follows the light or dark palette the reader is already in rather
// than carrying its own.

// sparkline draws a filled line chart of values over time.
//
// Returns "" for fewer than two points: a single point is not a trend, and
// drawing it as one implies a shape the data does not have.
func sparkline(values []float64, labels []string, color string) string {
	if len(values) < 2 {
		return ""
	}

	const (
		width  = 640
		height = 140
		padX   = 4
		padY   = 8
	)

	max := 0.0
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = 1 // a flat zero line still renders, rather than dividing by zero
	}

	stepX := float64(width-2*padX) / float64(len(values)-1)
	plotY := func(v float64) float64 {
		return float64(height-padY) - (v/max)*float64(height-2*padY)
	}

	var line, area strings.Builder
	for i, v := range values {
		x := float64(padX) + float64(i)*stepX
		y := plotY(v)
		fmt.Fprintf(&line, "%.1f,%.1f ", x, y)
		if i == 0 {
			fmt.Fprintf(&area, "%.1f,%.1f %.1f,%.1f ", x, float64(height-padY), x, y)
		} else {
			fmt.Fprintf(&area, "%.1f,%.1f ", x, y)
		}
	}
	fmt.Fprintf(&area, "%.1f,%.1f", float64(padX)+float64(len(values)-1)*stepX, float64(height-padY))

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" preserveAspectRatio="none" role="img">`, width, height)
	fmt.Fprintf(&b, `<polygon points="%s" fill="%s" opacity="0.12"/>`, strings.TrimSpace(area.String()), color)
	fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2"/>`,
		strings.TrimSpace(line.String()), color)
	b.WriteString("</svg>")

	// Endpoints only. A label per day is unreadable at any real history
	// length, and the numbers themselves are in the table below.
	if len(labels) >= 2 {
		fmt.Fprintf(&b, `<div class="chart-axis"><span>%s</span><span>%s</span></div>`,
			html.EscapeString(labels[0]), html.EscapeString(labels[len(labels)-1]))
	}
	return b.String()
}

// barRow draws one labelled horizontal bar with its count.
func barRow(label string, count, max int, color string) string {
	pct := 0
	if max > 0 {
		pct = count * 100 / max
	}
	return fmt.Sprintf(
		`<tr><td class="bar-label">%s</td><td class="bar-count">%d</td>`+
			`<td class="bar-cell"><div class="bar" style="width:%d%%;background:%s"></div></td></tr>`,
		html.EscapeString(label), count, pct, color)
}

// stackedBar draws one bar split into labelled segments.
//
// Segments carry a title attribute so a reader can hover for the exact
// number — the only interactivity here, and it needs no JavaScript.
func stackedBar(segments []Segment) string {
	total := 0
	for _, s := range segments {
		total += s.Count
	}
	if total == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<div class="stack">`)
	for _, s := range segments {
		if s.Count == 0 {
			continue
		}
		fmt.Fprintf(&b, `<div class="stack-seg" style="width:%.2f%%;background:%s" title="%s: %d"></div>`,
			float64(s.Count)*100/float64(total), s.Color, html.EscapeString(s.Label), s.Count)
	}
	b.WriteString(`</div><div class="stack-key">`)
	for _, s := range segments {
		if s.Count == 0 {
			continue
		}
		fmt.Fprintf(&b, `<span class="key"><i style="background:%s"></i>%s <b>%d</b></span>`,
			s.Color, html.EscapeString(s.Label), s.Count)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// Segment is one part of a stacked bar.
type Segment struct {
	Label string
	Count int
	Color string
}
