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

// warnLocalOnly says what running without a sync target costs.
//
// Local-only is a supported way to run whodunit and this is not an error:
// `dun verify` reports an unconfigured sync as a fact and keeps its exit
// code clean. But "supported" and "as useful" are different claims, and
// only the first was being made.
//
// What it costs is correlation. whodunit knows what an agent wrote and
// nothing about what happened to that work afterwards — whether it shipped,
// whether it broke, how long it took. Those facts live in GitHub and the
// issue tracker, and joining them to attribution is what a shared database
// is for. The dashboards that ship with this repository are that join.
func warnLocalOnly(w io.Writer, cfg config.Config) {
	if cfg.Sync.Configured() {
		return
	}
	c := termcolor.New(w)

	// What is missed comes first, and what is at risk second.
	//
	// An earlier version led with data loss, which is the weaker argument
	// and the less true one: the journal is regenerable, so "you will lose
	// your data" overstates it. The stronger case is that whodunit alone
	// cannot answer the question it exists for. It knows what the agent
	// wrote; it does not know what shipped, what broke, or how long
	// anything took. Those live in GitHub and the issue tracker, and the
	// join only happens in a shared database.
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", c.S(termcolor.Bad, "! attribution is only on this machine"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"whodunit measures what the agent wrote. It cannot see what shipped,"))
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"what broke, or how long the work took — that is in GitHub and your"))
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"issue tracker. Publishing joins them."))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted, "without a shared database you cannot ask:"))
	for _, q := range []string{
		"does AI-assisted work reach production faster",
		"do assisted changes fail more often, or less",
		"how much of a release was agent-written",
		"how any of this compares across repositories or people",
	} {
		fmt.Fprintf(w, "    %s %s\n", c.S(termcolor.Muted, "·"), c.S(termcolor.Muted, q))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		"six dashboards ship with it, ready to import."))
	fmt.Fprintln(w)

	// The durability half, second and briefly. Still worth saying, and
	// bounded by what is actually true rather than overclaimed.
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted, "it is also the only second copy:"))
	fmt.Fprintf(w, "    %s %s\n",
		c.S(termcolor.Good, "recoverable"),
		c.S(termcolor.Muted, "the journal — `dun ingest` rebuilds it, while the"))
	fmt.Fprintf(w, "    %s %s\n", c.S(termcolor.Muted, "           "),
		c.S(termcolor.Muted, "agents' own transcripts still exist"))
	fmt.Fprintf(w, "    %s %s\n",
		c.S(termcolor.Bad, "gone       "),
		c.S(termcolor.Muted, "pre-adoption baselines, and any commit whose"))
	fmt.Fprintf(w, "    %s %s\n", c.S(termcolor.Muted, "           "),
		c.S(termcolor.Muted, "transcript has since been pruned"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s  %s\n",
		c.S(termcolor.Bold, "dun config datalake"),
		c.S(termcolor.Muted, "connect one — DevLake, or any MySQL you control"))
}
