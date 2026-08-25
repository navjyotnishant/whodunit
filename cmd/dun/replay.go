// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: Re-run attributions that failed, from the replay log.

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/navjyotnishant/whodunit/internal/attribution"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/replaylog"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newReplayCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Show attributions that failed, and optionally retry them.",
		Long: "Lists determinations that produced no attribution because something\n" +
			"went wrong, rather than because there was nothing to find.\n\n" +
			"Two statuses reach this list. `unmatched` means an agent was active\n" +
			"but touched none of the staged files — usually correct, because a\n" +
			"generated file has no tool call naming it. `degraded` means\n" +
			"attribution itself failed, and is the one worth acting on.\n\n" +
			"Reports by default and changes nothing. --apply retries each one\n" +
			"and records the outcome.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReplay(cmd.OutOrStdout(), apply)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false,
		"retry each failure and record the outcome (default: report only)")
	return cmd
}

func runReplay(w io.Writer, apply bool) error {
	home, err := config.Dir()
	if err != nil {
		return fmt.Errorf("read config dir: %w", err)
	}

	out, err := replaylog.Outstanding(home)
	if err != nil {
		return fmt.Errorf("read replay log: %w", err)
	}

	c := termcolor.New(w)
	if len(out) == 0 {
		fmt.Fprintln(w, "nothing outstanding — no attribution has failed unrecoverably")
		return nil
	}

	// Counted by status before listing, because the two mean different
	// things and only one is a fault. A reader who sees "12 outstanding"
	// should not have to scan the list to learn whether any of it matters.
	byStatus := map[spec.Status]int{}
	for _, e := range out {
		byStatus[e.Status]++
	}
	fmt.Fprintf(w, "%d outstanding:\n", len(out))
	for _, st := range []spec.Status{spec.StatusDegraded, spec.StatusUnmatched} {
		if n := byStatus[st]; n > 0 {
			fmt.Fprintf(w, "  %s %4d   %s\n",
				c.S(termcolor.Muted, fmt.Sprintf("%-13s", st)), n,
				c.S(termcolor.Muted, st.Explain()))
		}
	}

	fmt.Fprintln(w)
	for _, e := range out {
		id := e.CommitSHA
		if id == "" {
			// Written before the commit existed, so there is no SHA to
			// name it by. Said plainly rather than printed as an empty
			// column: a replay cannot target this entry, and the reader
			// should know that rather than wonder.
			id = "(no commit — hook ran before the commit was written)"
		} else if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(w, "  %s  %s  %s\n",
			e.Time.Format(time.RFC3339), e.Status, id)
		if e.Err != "" {
			fmt.Fprintf(w, "      %s\n", c.S(termcolor.Muted, e.Err))
		}
		if len(e.StagedFiles) > 0 {
			fmt.Fprintf(w, "      %s\n", c.S(termcolor.Muted,
				fmt.Sprintf("%d staged file(s), %d agent line(s) available",
					len(e.StagedFiles), e.AgentLines)))
		}
	}

	if !apply {
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
			"reported only — nothing was changed. Re-run with --apply to retry."))
		return nil
	}

	return applyReplay(w, c, home, out)
}

// applyReplay re-runs each outstanding determination against the journal
// as it stands now.
//
// The journal accumulates: a transcript ingested after the commit was made
// carries line hashes that were not available at the time, which is
// exactly the case worth retrying. Reading the same window the hook read -
// LookbackWindow ending at the failure - asks the same question of a
// better-informed journal rather than a different question.
//
// The outcome is APPENDED whether it changed or not. An entry that still
// fails stays outstanding; one that now resolves is cancelled by a new
// entry rather than by editing the old one, so the record of what went
// wrong survives being fixed.
func applyReplay(w io.Writer, c *termcolor.Writer, home string, out []replaylog.Entry) error {
	dataDir, err := journalDataDir()
	if err != nil {
		return fmt.Errorf("read journal: %w", err)
	}

	var resolved, still int
	for _, e := range out {
		if e.CommitSHA == "" {
			// Nothing to mark resolved: without a SHA there is no key to
			// cancel the entry by, so retrying it would append a
			// duplicate rather than close anything.
			still++
			continue
		}

		since := e.Time.Add(-attribution.LookbackWindow)
		entries, rerr := journal.ReadRange(dataDir, e.RepoID, since, e.Time)
		if rerr != nil {
			still++
			continue
		}
		hashes, herr := journal.ReadLineHashes(dataDir, e.RepoID, since)
		if herr != nil {
			hashes = map[uint64]struct{}{}
		}

		// No staged evidence: the diff is long gone, so a ratio cannot be
		// recomputed and is omitted rather than guessed. The question
		// being asked is only whether an agent can now be matched at all.
		t := attribution.Determine(entries, e.StagedFiles, hashes,
			attribution.StagedEvidence{}, e.Time)

		if !t.Status.Attributed() {
			still++
			continue
		}
		resolved++
		replaylog.Record(home, replaylog.Entry{
			CommitSHA: e.CommitSHA,
			RepoID:    e.RepoID,
			Time:      time.Now(),
			Status:    e.Status,
			Replayed:  true,
		})
		fmt.Fprintf(w, "  resolved %s → %s\n", e.CommitSHA[:8], t.Method)
	}

	fmt.Fprintf(w, "\n%d resolved, %d still outstanding\n", resolved, still)
	if resolved > 0 {
		// The trailer in the commit is immutable and stays wrong. What
		// changed is the record of what is outstanding, so say what a
		// reader can actually do with that rather than implying the
		// commit was corrected.
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"the commits keep their original trailers - git history is not "+
				"rewritten. These are recorded as resolved so they stop "+
				"appearing as outstanding failures."))
	}
	return nil
}
