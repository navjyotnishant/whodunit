// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: What running without a sync target actually risks.

package main

import (
	"fmt"
	"io"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// warnLocalOnly says what is at risk when nothing is published.
//
// Local-only is a supported way to run whodunit and this is deliberately not
// an error: `dun verify` reports an unconfigured sync as a fact and keeps its
// exit code clean, and that stays true. But "supported" and "safe" are
// different claims, and only the first was being made.
//
// The wording is bounded by what is actually true, which is narrower than it
// first appears:
//
//   - The journal is regenerable. `dun ingest` rebuilds it from the agents'
//     own transcripts, so losing ~/.whodunit is not automatically losing the
//     data.
//   - What that recovery depends on is the transcripts still existing. Agents
//     prune their own history on their own schedules, which whodunit does not
//     control and cannot extend.
//   - Baselines are not regenerable at all. A pre-adoption snapshot measures
//     a window that has closed; no amount of re-ingesting brings it back.
//
// So the honest warning is about the second and third points, not a blanket
// "you will lose your data".
func warnLocalOnly(w io.Writer, cfg config.Config) {
	if cfg.Sync.Configured() {
		return
	}
	c := termcolor.New(w)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", c.S(termcolor.Bad, "! attribution is only on this machine"))
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"nothing is published, so there is no second copy of any of it."))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted, "if ~/.whodunit is lost:"))
	fmt.Fprintf(w, "    %s %s\n",
		c.S(termcolor.Good, "recoverable  "),
		c.S(termcolor.Muted, "the journal — `dun ingest` rebuilds it from agent transcripts,"))
	fmt.Fprintf(w, "    %s %s\n", "             ",
		c.S(termcolor.Muted, "for as long as those transcripts still exist"))
	fmt.Fprintf(w, "    %s %s\n",
		c.S(termcolor.Bad, "gone         "),
		c.S(termcolor.Muted, "pre-adoption baselines — they measure a window that has"))
	fmt.Fprintf(w, "    %s %s\n", "             ",
		c.S(termcolor.Muted, "closed, and re-ingesting cannot recreate one"))
	fmt.Fprintf(w, "    %s %s\n",
		c.S(termcolor.Bad, "gone         "),
		c.S(termcolor.Muted, "any commit whose agent transcript has since been pruned —"))
	fmt.Fprintf(w, "    %s %s\n", "             ",
		c.S(termcolor.Muted, "agents expire their own history, on their own schedule"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s  %s\n",
		c.S(termcolor.Bold, "dun config datalake"),
		c.S(termcolor.Muted, "publish to a shared database"))
	fmt.Fprintf(w, "  %s  %s\n",
		c.S(termcolor.Bold, "back up ~/.whodunit "),
		c.S(termcolor.Muted, "if local-only is the deliberate choice"))
}
