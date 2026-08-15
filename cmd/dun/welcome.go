// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The screen `dun` prints when run with no arguments.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

// runWelcome prints what dun is, whether this repository is instrumented,
// and the one command to run next.
//
// Cobra's default help lists twelve commands with no indication of which
// one applies right now. Someone running `dun` for the first time needs the
// next step, not the full surface.
func runWelcome(out io.Writer, root *cobra.Command) error {
	w := termcolor.New(out)

	renderWordmark(out, w)

	// The three states, framed. Boxing the part that changes gives the eye
	// somewhere to land: everything above is constant across runs, and this
	// is the only section that answers "where am I".
	state, detail := repoState()
	var status string
	var lines [][2]string

	switch state {
	case repoInstrumented:
		status = w.S(termcolor.Good, "✔") + " this repository is instrumented"
		lines = [][2]string{
			{"dun status", "coverage and method mix for recent commits"},
			{"dun report", "a full HTML report"},
		}
	case repoNotInstrumented:
		status = w.S(termcolor.Muted, "·") + " this repository is not instrumented yet"
		lines = [][2]string{
			{"dun init", "install the hooks here"},
		}
	default:
		status = w.S(termcolor.Muted, "·") + " not inside a git repository" + detail
		lines = [][2]string{
			{"dun repos list", "repositories you have instrumented"},
		}
	}
	renderPanel(out, w, status, lines)

	// The command list, after the orientation above rather than instead of
	// it. Knowing where you stand answers "what do I run next"; knowing the
	// commands answers "what else is there". Printing only the first left
	// people running `dun --help` to discover the surface, which is a step
	// that did not need to exist.
	fmt.Fprintln(out)
	renderCommandList(out, w, root)

	// One line, not the twelve `dun status` prints. This screen is seen on
	// every bare invocation, and a warning that fills it gets scrolled past
	// — which is how a warning stops working. The detail is one command
	// away and named here.
	if cfg, err := config.Load(); err == nil && !cfg.Sync.Configured() {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  %s %s\n",
			w.S(termcolor.Bad, "!"),
			w.S(termcolor.Muted, "nothing is published — no delivery correlation, no second copy"))
		fmt.Fprintf(out, "    %s\n",
			w.S(termcolor.Muted, "dun status   what that costs, and what would fix it"))
	}

	// Only on bare `dun`, and only from here (NAV-76, criterion 10).
	//
	// Not on every command, and never from a hook: this is the one screen
	// someone reaches by typing the tool's name with no task in mind, which
	// makes it the one place a notice about the tool itself belongs. The
	// commit path must stay free of network calls entirely — a version
	// check that can hang a commit is worse than a stale binary.
	//
	// Silent when there is nothing to say, when the check is disabled, and
	// on any failure, so a machine with no outbound access sees no
	// difference (criteria 7, 11, 12).
	// Buffered rather than written straight through, so "nothing to say"
	// costs no blank line either — a stray gap is the same kind of noise as
	// a stray message, just quieter.
	if cfg, err := config.Load(); err == nil {
		var notice strings.Builder
		reportNewerVersion(&notice, w, cfg, version)
		if notice.Len() > 0 {
			fmt.Fprintln(out)
			fmt.Fprint(out, notice.String())
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, w.S(termcolor.Muted, "  dun --help for the full surface, dun <command> --help for one"))
	return nil
}

type repoStatus int

const (
	repoUnknown repoStatus = iota
	repoInstrumented
	repoNotInstrumented
)

// repoState answers whether the current directory is an instrumented
// repository, degrading to repoUnknown rather than failing: the welcome
// screen is the first thing a new user sees, and it must render even when
// there is no git repo, no registry, and nothing recorded.
func repoState() (repoStatus, string) {
	id, err := currentRepoID()
	if err != nil {
		return repoUnknown, ""
	}
	entries, err := registry.List()
	if err != nil {
		return repoUnknown, ""
	}
	for _, e := range entries {
		if e.RepoID == id {
			return repoInstrumented, ""
		}
	}
	return repoNotInstrumented, ""
}

// renderWordmark prints the name, the version, and one line of what this is.
//
// A wordmark rather than a sentence because the first thing on screen should
// say what you are looking at before it says anything about your repository.
// The version is beside it because a dev build and a release behave
// differently — hook staleness is computed from it — and "which one am I
// running" should not need a second command.
func renderWordmark(out io.Writer, w *termcolor.Writer) {
	for _, line := range []string{
		`           _               _             _ _ `,
		` __      _| |__   ___   __| |_   _ _ __ (_) |_`,
		` \ \ /\ / / '_ \ / _ \ / _` + "`" + ` | | | | '_ \| | __|`,
		`  \ V  V /| | | | (_) | (_| | |_| | | | | | |_`,
		`   \_/\_/ |_| |_|\___/ \__,_|\__,_|_| |_|_|\__|`,
	} {
		fmt.Fprintln(out, w.S(termcolor.Bold, line))
	}
	fmt.Fprintln(out)
	// A release reads "v0.2.0"; a local build reads "dev" without the v,
	// because "vdev" looks like a typo rather than a state.
	version := Version()
	if IsRelease() {
		version = "v" + version
	}
	// The name in text as well as in the wordmark. Figlet letters are not
	// a string: they do not survive a grep, a screen reader, or a terminal
	// that renders the box-drawing characters as boxes.
	fmt.Fprintf(out, "  %s %s  %s\n",
		w.S(termcolor.Bold, "whodunit"),
		w.S(termcolor.Muted, version),
		w.S(termcolor.Muted, "— AI adoption, measured against what shipped"))
	fmt.Fprintln(out)
}

// renderPanel draws a box around the current state and its next commands.
//
// Width is computed from the content rather than fixed: a hardcoded width
// either wraps on a narrow terminal or leaves a gap on a wide one, and the
// longest line here varies with the repository path.
func renderPanel(out io.Writer, w *termcolor.Writer, status string, lines [][2]string) {
	// Measured without styling. Escape sequences are zero-width on screen
	// and several bytes to len(), so measuring the styled string would
	// draw a box far wider than the text inside it.
	width := len(termcolor.Strip(status))
	for _, l := range lines {
		if n := len(l[0]) + 3 + len(l[1]); n > width {
			width = n
		}
	}
	width += 4

	bar := strings.Repeat("─", width)
	fmt.Fprintf(out, "  %s\n", w.S(termcolor.Muted, "┌"+bar+"┐"))
	fmt.Fprintf(out, "  %s %-*s %s\n", w.S(termcolor.Muted, "│"),
		width-2+len(status)-len(termcolor.Strip(status)), status,
		w.S(termcolor.Muted, "│"))
	if len(lines) > 0 {
		fmt.Fprintf(out, "  %s%s%s\n", w.S(termcolor.Muted, "│"),
			strings.Repeat(" ", width), w.S(termcolor.Muted, "│"))
	}
	for _, l := range lines {
		cmd := w.S(termcolor.Bold, l[0])
		pad := width - 2 - len(l[0]) - 3 - len(l[1])
		fmt.Fprintf(out, "  %s %s   %s%s %s\n", w.S(termcolor.Muted, "│"),
			cmd, l[1], strings.Repeat(" ", pad), w.S(termcolor.Muted, "│"))
	}
	fmt.Fprintf(out, "  %s\n", w.S(termcolor.Muted, "└"+bar+"┘"))
}

// renderCommandList prints every command cobra knows about.
//
// Read from the command tree rather than written out here. The list used to
// be a hand-picked eight, which drifted the moment anything was added:
// `baseline`, `delta` and `check` were all user-facing and missing, while
// `dun --help` listed eighteen. A screen that claims to show the commands
// and shows two-thirds of them is worse than one that sends you to --help.
//
// Cobra's own housekeeping commands are dropped, along with anything marked
// hidden — `help` and `completion` are noise on a first screen, and the
// hook entry point is not something anyone types.
func renderCommandList(out io.Writer, w *termcolor.Writer, root *cobra.Command) {
	if root == nil {
		return
	}

	var cmds []*cobra.Command
	width := 0
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		cmds = append(cmds, c)
		if n := len(c.Name()); n > width {
			width = n
		}
	}
	if len(cmds) == 0 {
		return
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, w.S(termcolor.Muted, "  commands"))
	for _, c := range cmds {
		fmt.Fprintf(out, "    %s %s\n",
			w.S(termcolor.Bold, fmt.Sprintf("%-*s", width, c.Name())),
			w.S(termcolor.Muted, firstSentence(c.Short)))
	}
}

// firstSentence trims a Short to what fits on a line beside a command name.
func firstSentence(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	const max = 58
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}
