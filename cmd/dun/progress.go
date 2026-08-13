// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: A progress bar for work long enough that silence reads as a
// hang.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// progressBar draws a single self-overwriting line.
//
// Only when the output is a terminal. Redrawing with a carriage return into
// a pipe produces one enormous line with the bar's whole history in it, and
// into a CI log it produces thousands — which is why this checks rather than
// assuming.
type progressBar struct {
	w     io.Writer
	c     *termcolor.Writer
	label string
	width int

	live bool
	last time.Time
	done bool
}

// newProgressBar returns a bar that draws to w, or a silent one when w is
// not an interactive terminal.
func newProgressBar(w io.Writer, label string) *progressBar {
	return &progressBar{
		w:     w,
		c:     termcolor.New(w),
		label: label,
		width: 28,
		live:  isTerminalWriter(w),
	}
}

// Update redraws the bar, at most about twenty times a second.
//
// Throttled because the callback fires per row: on sixteen thousand rows an
// unthrottled redraw spends more time formatting escape sequences than the
// database spends writing, which would make the progress bar the slow part.
func (p *progressBar) Update(done, total int) {
	if !p.live || p.done || total <= 0 {
		return
	}
	now := time.Now()
	if done < total && now.Sub(p.last) < 50*time.Millisecond {
		return
	}
	p.last = now

	pct := float64(done) / float64(total)
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(p.width))

	fmt.Fprintf(p.w, "\r  %s %s%s %3.0f%% %s",
		p.c.S(termcolor.Muted, p.label),
		p.c.S(termcolor.Good, strings.Repeat("█", filled)),
		p.c.S(termcolor.Muted, strings.Repeat("░", p.width-filled)),
		pct*100,
		p.c.S(termcolor.Muted, fmt.Sprintf("%d/%d", done, total)))
}

// Done clears the bar so the line beneath it is not written over a partial
// redraw.
func (p *progressBar) Done() {
	if !p.live || p.done {
		return
	}
	p.done = true
	// Overwrite with spaces rather than printing a newline: leaving a
	// finished bar on screen is noise once the summary below says the same
	// thing with real numbers.
	fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", p.width+40))
}

// isTerminalWriter reports whether w is an interactive terminal.
//
// The existing isTerminal answers this for a reader, because prompts need
// it. This is the same question in the other direction: anything that is not
// a character device — a pipe, a file, a CI log — gets no bar, since a
// carriage-return redraw there produces one line holding every frame.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
