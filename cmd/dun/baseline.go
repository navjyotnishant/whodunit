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

func newBaselineCaptureCmd() *cobra.Command {
	var (
		windowDays        int
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
			"PR throughput and change-failure rate cannot be read from git, so they are\n" +
			"optional flags you supply from GitHub Insights or your CI dashboard. Unset\n" +
			"flags are omitted from the snapshot rather than recorded as zero.",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			snap, err := baseline.Capture(windowDays, manual, time.Now())
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
			if err := baseline.Write(out, snap); err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "captured baseline over the last %d days\n", snap.WindowDays)
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

	cmd.Flags().IntVar(&windowDays, "days", 90, "how many days of history to measure")
	cmd.Flags().StringVar(&out, "out", "", "output path (default: alongside the journal, outside the worktree)")
	cmd.Flags().IntVar(&prsMerged, "prs-merged", 0, "PRs merged in the window (from GitHub/GitLab; not derivable from git)")
	cmd.Flags().Float64Var(&medianCycleHrs, "median-cycle-hours", 0, "median PR cycle time in hours (not derivable from git)")
	cmd.Flags().Float64Var(&changeFailureRate, "change-failure-rate", 0, "change failure rate 0-1 (from CI/deploy data; not derivable from git)")
	cmd.Flags().StringVar(&note, "note", "", "free-text note recorded with the snapshot")

	return cmd
}
