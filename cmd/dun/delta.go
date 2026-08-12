package main

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/navjyotnishant/whodunit/internal/delta"
	"github.com/spf13/cobra"
)

func newDeltaCmd() *cobra.Command {
	var (
		windowDays   int
		baselinePath string
	)

	cmd := &cobra.Command{
		Use:   "delta",
		Short: "Compare delivery metrics before and after AI-attribution adoption.",
		Long: "Reports two independent cuts, because either alone misleads:\n\n" +
			"  cross-period   the pre-adoption baseline against a recent window.\n" +
			"                 Shows change over time, but every difference is\n" +
			"                 attributed to adoption, so it is a correlation and\n" +
			"                 never a cause.\n\n" +
			"  within-period  assisted commits against undetermined commits inside\n" +
			"                 the same window. Controls for calendar effects the\n" +
			"                 cross-period cut cannot.\n\n" +
			"Revert rate is always shown next to throughput. A velocity gain that\n" +
			"arrives with more reverts is deferred rework, not speed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if baselinePath == "" {
				dir, err := journalDir()
				if err != nil {
					return err
				}
				baselinePath = filepath.Join(filepath.Dir(dir), "baseline.json")
			}

			base, err := baseline.Load(baselinePath)
			if err != nil {
				return err
			}

			res, err := delta.Compute(base, windowDays, time.Now())
			if err != nil {
				return err
			}

			renderDelta(cmd.OutOrStdout(), res)
			return nil
		},
	}

	cmd.Flags().IntVar(&windowDays, "days", 90, "size of the recent window to measure")
	cmd.Flags().StringVar(&baselinePath, "baseline", "", "path to a baseline snapshot (default: alongside the journal)")
	return cmd
}

func renderDelta(w io.Writer, res delta.Result) {
	fmt.Fprintf(w, "Within-period (last %d days)\n", res.Within.WindowDays)
	fmt.Fprintln(w, "  Comparing assisted vs undetermined commits in the same window.")
	fmt.Fprintln(w, "  This cut controls for calendar effects; it does not control for")
	fmt.Fprintln(w, "  which work each group was chosen for.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-16s %10s %10s\n", "", "assisted", "undet.")
	fmt.Fprintf(w, "  %-16s %10d %10d\n", "commits", res.Within.Assisted.Commits, res.Within.Undetermined.Commits)
	fmt.Fprintf(w, "  %-16s %10.1f %10.1f\n", "commits/week", res.Within.Assisted.CommitsPerWeek, res.Within.Undetermined.CommitsPerWeek)
	fmt.Fprintf(w, "  %-16s %10d %10d\n", "median diff", res.Within.Assisted.MedianDiffLines, res.Within.Undetermined.MedianDiffLines)
	fmt.Fprintf(w, "  %-16s %9.1f%% %9.1f%%\n", "revert rate", res.Within.Assisted.RevertRate*100, res.Within.Undetermined.RevertRate*100)

	if res.Cross != nil {
		fmt.Fprintf(w, "\nCross-period (baseline captured %s)\n", res.Cross.BaselineCapturedAt)
		fmt.Fprintf(w, "  %-16s %10s %10s %10s\n", "", "baseline", "current", "change")
		printCrossRow(w, "commits/week", res.Cross.Baseline.CommitsPerWeek, res.Cross.Current.CommitsPerWeek)
		printCrossRow(w, "median diff", float64(res.Cross.Baseline.MedianDiffLines), float64(res.Cross.Current.MedianDiffLines))
		printCrossRow(w, "revert rate %", res.Cross.Baseline.RevertRate*100, res.Cross.Current.RevertRate*100)

		fmt.Fprintln(w, "\n  These are correlations, not causes. All of the following move the")
		fmt.Fprintln(w, "  same numbers independently of AI adoption:")
		for _, c := range res.Cross.Confounders {
			fmt.Fprintf(w, "    - %s\n", c)
		}
	}

	if len(res.Warnings) > 0 {
		fmt.Fprintln(w)
		for _, warn := range res.Warnings {
			fmt.Fprintf(w, "! %s\n", warn)
		}
	}
}

func printCrossRow(w io.Writer, label string, before, after float64) {
	change := "n/a"
	if pct, ok := delta.PercentChange(before, after); ok {
		change = fmt.Sprintf("%+.0f%%", pct*100)
	}
	fmt.Fprintf(w, "  %-16s %10.1f %10.1f %10s\n", label, before, after, change)
}
