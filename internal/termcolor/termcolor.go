// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: ANSI styling for dun's console output, disabled unless the
// destination is a terminal that wants color.

// Package termcolor styles console output, and knows when not to.
//
// Every command writes through the same Writer, so the decision of whether
// color is appropriate is made once here rather than at each call site. A
// command added later cannot forget to check: it gets a Writer, and the
// Writer already knows.
//
// Color is emitted only when the destination is a terminal and no
// environment variable objects. Piped output, CI logs, and the commit
// message file are therefore plain by construction — which matters more
// than the color does, since dun writes into COMMIT_EDITMSG and into the
// output of a CI gate.
package termcolor

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Style is an ANSI SGR sequence, or the empty string when color is off.
type Style string

// The confidence ladder, matching the HTML report's palette so a method
// reads the same in the terminal as it does in the report. 256-color codes
// are the closest match to the hex values in internal/report.
//
// Undetermined is gray, never red: it is an honest unknown rather than a
// failure state (NAV-21). Coloring it like an error would assert exactly
// the thing the spec refuses to assert.
const (
	Intersected  Style = "\x1b[38;5;34m"  // #16a34a strongest evidence
	Observed     Style = "\x1b[38;5;70m"  // #65a30d
	Inferred     Style = "\x1b[38;5;178m" // #ca8a04
	Declared     Style = "\x1b[38;5;166m" // #ea580c
	Undetermined Style = "\x1b[38;5;244m" // #71717a honest unknown

	Bold  Style = "\x1b[1m"
	Dim   Style = "\x1b[2m"
	Warn  Style = "\x1b[38;5;178m" // warnings share the "attention" hue
	Good  Style = "\x1b[38;5;34m"
	Muted Style = "\x1b[38;5;244m"

	// Bad is for a genuine fault, distinct from Warn. A recovered panic and
	// an unreadable transcript are both survivable, and reading as the same
	// severity would bury the one worth acting on.
	Bad Style = "\x1b[38;5;160m"

	reset Style = "\x1b[0m"
)

// Writer wraps an output stream and styles text only when appropriate.
// The zero value is usable and emits no color.
type Writer struct {
	io.Writer
	enabled bool
}

// New returns a Writer for w, enabling color only if w is a terminal and
// the environment does not opt out.
//
// The io.Writer is checked for an Fd method rather than assumed to be
// os.Stdout, because tests and hooks pass buffers and files — both of
// which must come out plain.
func New(w io.Writer) *Writer {
	return &Writer{Writer: w, enabled: shouldColor(w)}
}

// Plain returns a Writer that never emits color, whatever the destination.
// Used by the git hooks and by `dun check`: the commit message file and a
// CI gate's output must stay machine-readable regardless of terminal.
func Plain(w io.Writer) *Writer {
	return &Writer{Writer: w, enabled: false}
}

// Enabled reports whether this Writer will emit escape sequences.
func (w *Writer) Enabled() bool { return w != nil && w.enabled }

// S styles s, or returns it unchanged when color is off.
//
// Always pairs the style with a reset, so a styled value cannot leak its
// color into whatever is printed after it.
func (w *Writer) S(style Style, s string) string {
	if !w.Enabled() || style == "" {
		return s
	}
	return string(style) + s + string(reset)
}

// shouldColor applies the conventions a terminal user expects, in the
// order they take precedence.
func shouldColor(w io.Writer) bool {
	// https://no-color.org — set to anything, color is off. Checked
	// before FORCE_COLOR on purpose: when a user has said both, the
	// refusal wins. Opting out of color is a stronger statement than
	// opting in, and honoring the opt-in here would let an inherited
	// FORCE_COLOR override an explicit NO_COLOR in the same shell.
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	// An explicit request beats detection, so a user can force color
	// through a pipe (into `less -R`, say).
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	// A terminal that says it cannot do color is telling the truth.
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// CI logs are captured, not watched. Most CI providers set this.
	if os.Getenv("CI") != "" {
		return false
	}

	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false // a buffer or a file: not a terminal
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// MethodStyle maps an attribution method to its color. Unknown methods get
// the undetermined gray rather than no style, so an unrecognized value
// still reads as "no evidence" instead of silently looking like prose.
func MethodStyle(method string) Style {
	switch method {
	case "intersected":
		return Intersected
	case "observed":
		return Observed
	case "inferred":
		return Inferred
	case "declared":
		return Declared
	default:
		return Undetermined
	}
}
