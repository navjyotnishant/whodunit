// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The screen `dun` prints when run with no arguments.

package main

import (
	"fmt"
	"io"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// runWelcome prints what dun is, whether this repository is instrumented,
// and the one command to run next.
//
// Cobra's default help lists twelve commands with no indication of which
// one applies right now. Someone running `dun` for the first time needs the
// next step, not the full surface.
func runWelcome(out io.Writer) error {
	w := termcolor.New(out)

	fmt.Fprintln(out, w.S(termcolor.Bold, "whodunit")+" — which agent touched this code, and how sure we are.")
	fmt.Fprintln(out)

	state, detail := repoState()
	switch state {
	case repoInstrumented:
		fmt.Fprintln(out, "  "+w.S(termcolor.Good, "✓")+" this repository is instrumented")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  "+w.S(termcolor.Bold, "dun status")+"   coverage and method mix for recent commits")
		fmt.Fprintln(out, "  "+w.S(termcolor.Bold, "dun report")+"   a full HTML report")
	case repoNotInstrumented:
		fmt.Fprintln(out, "  "+w.S(termcolor.Muted, "•")+" this repository is not instrumented yet")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  "+w.S(termcolor.Bold, "dun init")+"     install the hooks here")
	default:
		fmt.Fprintln(out, "  "+w.S(termcolor.Muted, "•")+" not inside a git repository"+detail)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  "+w.S(termcolor.Bold, "dun repos list")+"   repositories you have instrumented")
	}

	// The command list, after the orientation above rather than instead of
	// it. Knowing where you stand answers "what do I run next"; knowing the
	// commands answers "what else is there". Printing only the first left
	// people running `dun --help` to discover the surface, which is a step
	// that did not need to exist.
	fmt.Fprintln(out)
	fmt.Fprintln(out, w.S(termcolor.Muted, "  commands"))
	for _, c := range [][2]string{
		{"init", "instrument this repository"},
		{"status", "coverage, method mix, what would sync"},
		{"verify", "check everything is working"},
		{"report", "an HTML report you can open"},
		{"log", "what the hooks did, and what they swallowed"},
		{"journal", "the recorded events themselves"},
		{"sync", "publish to a shared database"},
		{"repos", "every repository you have instrumented"},
	} {
		fmt.Fprintf(out, "    %s %s\n",
			w.S(termcolor.Bold, fmt.Sprintf("%-9s", c[0])),
			w.S(termcolor.Muted, c[1]))
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
