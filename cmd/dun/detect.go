// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Renders agent detection results for `dun init` and
// `dun config agents`.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// printDetections reports what each agent's probe found.
//
// One renderer for both `dun init` and `dun config agents`: two code paths
// that could disagree about whether an agent is present is a bug waiting to
// be filed.
//
// verbose adds the path and where it came from — useful when diagnosing,
// noise during install.
func printDetections(w io.Writer, ds []adapter.Detection, verbose bool) {
	c := termcolor.New(w)
	found := 0

	for _, d := range ds {
		var mark, note string
		switch d.State {
		case adapter.StateFound:
			found++
			mark = c.S(termcolor.Good, "found")
			note = fmt.Sprintf("%d session%s", d.Sessions, plural3(d.Sessions))
		case adapter.StateEmpty:
			found++
			mark = c.S(termcolor.Good, "found")
			note = "no sessions for this repository yet"
		case adapter.StateNotInstalled:
			// No path: an agent someone does not use is not a problem, and
			// printing where it would have been is noise.
			mark = c.S(termcolor.Muted, "not found")
		case adapter.StateMissing:
			// The one state that is a mistake rather than a fact. Shows the
			// root — the value that was configured — not the per-repository
			// subdirectory derived from it, which the user never typed and
			// cannot act on.
			mark = c.S(termcolor.Warn, "path not found")
			note = d.Root
		case adapter.StateError:
			mark = c.S(termcolor.Warn, "unknown")
			note = d.Err.Error()
		}

		fmt.Fprintf(w, "  %-14s %-16s", d.Agent, mark)
		if verbose && d.Root != "" && d.State != adapter.StateMissing && d.State != adapter.StateNotInstalled {
			fmt.Fprintf(w, " %s", c.S(termcolor.Muted, d.Root))
			if d.Source != adapter.SourceDefault {
				fmt.Fprintf(w, " %s", c.S(termcolor.Muted, "("+string(d.Source)+")"))
			}
		}
		if note != "" {
			fmt.Fprintf(w, "  %s", c.S(termcolor.Muted, note))
		}
		fmt.Fprintln(w)
	}

	// Say how to fix it in the same breath as the bad news. A user who
	// sees their agent missing and no way to correct it just has a
	// complaint.
	if found < len(ds) {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
			"an agent stored somewhere else? point dun at it:"))
		fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
			"  dun config set agent.<name>.path <dir>"))
	}
}

// reportAgents probes and prints, for `dun init`.
//
// Never returns an error and never panics: a detection failure must not
// fail an install that already succeeded. An agent whose probe errors is
// reported as unknown, which is the honest answer.
func reportAgents(w io.Writer, repoPath string) {
	cwd := repoPath
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return // cannot probe without a directory; hooks are installed regardless
		}
	}

	ds := adapter.Detect(cwd)
	if len(ds) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "checking for AI agents")
	printDetections(w, ds, false)
}

func plural3(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
