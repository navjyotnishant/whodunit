package main

import (
	"fmt"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/spf13/cobra"
)

func newBaselineCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "baseline",
		Short: "Capture a pre-adoption snapshot of this repo's delivery metrics.",
	}
	root.AddCommand(newBaselineCaptureCmd())
	return root
}

// resolveWindow turns the flags into a span. --since wins over --days;
// --until defaults to now. The bool reports whether the user named the
// window, which decides how the result is phrased.
func resolveWindow(since, until string, days int, now time.Time) (baseline.Window, bool, error) {
	const layout = "2006-01-02"

	if since == "" && until == "" {
		return baseline.WindowFromDays(days, now), false, nil
	}

	w := baseline.Window{Since: now.AddDate(0, 0, -days), Until: now}
	if since != "" {
		t, err := time.ParseInLocation(layout, since, now.Location())
		if err != nil {
			return baseline.Window{}, false, fmt.Errorf("--since %q: expected YYYY-MM-DD", since)
		}
		w.Since = t
	}
	if until != "" {
		t, err := time.ParseInLocation(layout, until, now.Location())
		if err != nil {
			return baseline.Window{}, false, fmt.Errorf("--until %q: expected YYYY-MM-DD", until)
		}
		// A date means the whole of that day, not midnight at its start —
		// otherwise --until 2026-06-30 silently drops that day's commits.
		w.Until = t.AddDate(0, 0, 1)
	}
	return w, true, nil
}

// describeWindow renders a snapshot's span, falling back to the day count
// for snapshots captured before the bounds were recorded.
func describeWindow(s *baseline.Snapshot) string {
	if s.WindowSince.IsZero() || s.WindowUntil.IsZero() {
		return fmt.Sprintf("%d-day window", s.WindowDays)
	}
	return fmt.Sprintf("%s to %s", s.WindowSince.Format("2006-01-02"), s.WindowUntil.Format("2006-01-02"))
}

func newBaselineCaptureCmd() *cobra.Command {
	var (
		windowDays        int
		since             string
		until             string
		force             bool
		out               string
		prsMerged         int
		medianCycleHrs    float64
		changeFailureRate float64
		note              string
	)

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Record delivery metrics before dun's hooks are installed. Run this FIRST.",
		Long: "Captures a dated, immutable snapshot of this repository's delivery metrics.\n\n" +
			"Run this BEFORE `dun init` installs hooks: the pre-adoption window closes\n" +
			"permanently once attribution starts, and a before/after comparison has\n" +
			"nothing to compare against without it.\n\n" +
			"Name the window explicitly. Nothing is captured without one, because\n" +
			"the useful default does not exist: a window ending today stops being\n" +
			"pre-adoption the moment hooks are installed, so it would measure\n" +
			"AI-assisted work and record it as the before.\n\n" +
			"PR throughput and change-failure rate cannot be read from git, so they\n" +
			"are optional flags you supply from GitHub Insights or your CI dashboard.\n" +
			"Unset flags are omitted from the snapshot rather than recorded as zero.",
		Example: "  # The period you were working without an agent. This is the one\n" +
			"  # that matters, and the only one worth capturing after hooks land.\n" +
			"  dun baseline capture --since 2026-01-01 --until 2026-06-30\n\n" +
			"  # Open-ended: from a date up to today. Only pre-adoption if no\n" +
			"  # agent has touched the repository since.\n" +
			"  dun baseline capture --since 2026-01-01\n\n" +
			"  # The last N days, ending today. Correct only BEFORE `dun init`.\n" +
			"  dun baseline capture --days 90\n\n" +
			"  # Add the numbers git cannot see, read off GitHub or your CI.\n" +
			"  dun baseline capture --since 2026-01-01 --until 2026-06-30 \\\n" +
			"      --prs-merged 214 --median-cycle-hours 31.5 --change-failure-rate 0.08\n\n" +
			"  # Replace a snapshot captured over the wrong window. Prints what\n" +
			"  # it is about to destroy; refuses without --force.\n" +
			"  dun baseline capture --since 2026-02-01 --until 2026-05-31 --force",
		RunE: func(cmd *cobra.Command, args []string) error {
			// With no window given, print help rather than capturing.
			//
			// The old default measured 90 days ending today, which stops
			// being a pre-adoption window the moment hooks are installed —
			// so the no-argument path silently produced the one baseline
			// nobody wants: AI-assisted work recorded as the before. A
			// window is now a deliberate choice, not a default.
			if !cmd.Flags().Changed("since") && !cmd.Flags().Changed("until") &&
				!cmd.Flags().Changed("days") {
				return cmd.Help()
			}

			var manual *baseline.ManualMetrics
			if cmd.Flags().Changed("prs-merged") || cmd.Flags().Changed("median-cycle-hours") ||
				cmd.Flags().Changed("change-failure-rate") || cmd.Flags().Changed("note") {
				manual = &baseline.ManualMetrics{Note: note}
				if cmd.Flags().Changed("prs-merged") {
					manual.PRsMerged = &prsMerged
				}
				if cmd.Flags().Changed("median-cycle-hours") {
					manual.MedianCycleTimeHrs = &medianCycleHrs
				}
				if cmd.Flags().Changed("change-failure-rate") {
					manual.ChangeFailureRate = &changeFailureRate
				}
			}

			now := time.Now()
			window, explicit, err := resolveWindow(since, until, windowDays, now)
			if err != nil {
				return err
			}

			snap, err := baseline.CaptureWindow(window, manual, now)
			if err != nil {
				return err
			}

			if out == "" {
				p, err := defaultBaselinePath()
				if err != nil {
					return err
				}
				out = p
			}

			w := cmd.OutOrStdout()

			// Say what is being destroyed before destroying it. A baseline is
			// the only record of a window that cannot be recaptured, so a
			// silent replacement is worse than a refusal.
			if force {
				if prev, err := baseline.Load(out); err == nil {
					fmt.Fprintf(w, "replacing baseline captured %s: %s, %d commits\n\n",
						prev.CapturedAt.Format("2006-01-02"), describeWindow(prev), prev.Git.Commits)
				}
				err = baseline.WriteForce(out, snap)
			} else {
				err = baseline.Write(out, snap)
			}
			if err != nil {
				return err
			}

			if explicit {
				// A named --until is exclusive internally (the day after the
				// one given, so that day's commits count); echo the date the
				// user actually typed. An omitted one already ends now.
				shown := window.Until
				if until != "" {
					shown = shown.AddDate(0, 0, -1)
				}
				fmt.Fprintf(w, "captured baseline over %s to %s (%d days)\n",
					window.Since.Format("2006-01-02"), shown.Format("2006-01-02"), snap.WindowDays)
			} else {
				fmt.Fprintf(w, "captured baseline over the last %d days\n", snap.WindowDays)
			}
			fmt.Fprintf(w, "  commits:            %d (%.1f/week)\n", snap.Git.Commits, snap.Git.CommitsPerWeek)
			fmt.Fprintf(w, "  median diff:        %d lines\n", snap.Git.MedianDiffLines)
			fmt.Fprintf(w, "  reverts:            %d (%.1f%%)\n", snap.Git.Reverts, snap.Git.RevertRate*100)
			fmt.Fprintf(w, "  mean commit gap:    %.1f hours\n", snap.Git.MeanHoursBetween)
			if snap.Manual == nil {
				fmt.Fprintf(w, "\nNo PR/CI metrics supplied. Those can't be read from git — pass\n"+
					"--prs-merged / --median-cycle-hours / --change-failure-rate if you want\n"+
					"them in the snapshot.\n")
			}
			fmt.Fprintf(w, "\nwrote %s\n", out)
			return nil
		},
	}

	cmd.Flags().IntVar(&windowDays, "days", 90, "how many days of history to measure, ending today")
	cmd.Flags().StringVar(&since, "since", "", "window start, YYYY-MM-DD (overrides --days)")
	cmd.Flags().StringVar(&until, "until", "", "window end, YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing baseline, printing what it replaced")
	cmd.Flags().StringVar(&out, "out", "", "output path (default: alongside the journal, outside the worktree)")
	cmd.Flags().IntVar(&prsMerged, "prs-merged", 0, "PRs merged in the window (from GitHub/GitLab; not derivable from git)")
	cmd.Flags().Float64Var(&medianCycleHrs, "median-cycle-hours", 0, "median PR cycle time in hours (not derivable from git)")
	cmd.Flags().Float64Var(&changeFailureRate, "change-failure-rate", 0, "change failure rate 0-1 (from CI/deploy data; not derivable from git)")
	cmd.Flags().StringVar(&note, "note", "", "free-text note recorded with the snapshot")

	return cmd
}
