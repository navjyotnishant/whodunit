package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/spf13/cobra"
)

func newReportCmd() *cobra.Command {
	var out string
	var limit int
	var template string
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a self-contained local HTML report (no server, no network).",
		Long: "Renders a single HTML file with no external requests: it opens\n" +
			"offline, behind a firewall, and as an email attachment.\n\n" +
			"Three templates answer different questions:\n\n" +
			"  exec      is adoption growing, and is the work landing (default)\n" +
			"  adoption  who and what is being used — agents, tools, sessions\n" +
			"  detail    what exactly happened, per commit and per file",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Report reads the git log and the journal from the working
			// directory, so --repo means going there. Without it, running
			// this from anywhere else gave "read git log: exit status 128".
			restore, err := enterRepo(repoFlag, "report", "report on")
			if err != nil {
				return err
			}
			defer restore()

			tmpl, err := report.ParseTemplate(template)
			if err != nil {
				return err
			}

			stats, err := report.Collect(limit)
			if err != nil {
				return err
			}

			// Whether a baseline exists decides if the report says a
			// before/after comparison is unavailable. Only the command
			// layer knows where baselines live.
			if p, err := defaultBaselinePath(); err == nil {
				if _, err := os.Stat(p); err == nil {
					stats.HasBaseline = true
				}
			}

			// The journal half is best-effort. A repository with nothing
			// recorded still gets a report from its commit trailers, and
			// the template says the journal is empty rather than showing
			// zeros that read as "no AI was used" (NAV-21).
			var act report.Activity
			if dataDir, err := journalDataDir(); err == nil {
				if repoID, err := currentRepoID(); err == nil {
					act = report.CollectActivity(dataDir, repoID)
				}
			}

			var b strings.Builder
			report.RenderTemplate(&b, stats, act, tmpl)
			if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
				return fmt.Errorf("write report: %w", err)
			}

			absOut, err := filepath.Abs(out)
			if err != nil {
				absOut = out // fall back to whatever was given rather than fail the report
			}
			// A fragment carrying the write time, so the URL differs on
			// every run.
			//
			// The report is written to the same filename each time, which
			// means a browser serves its cached copy and a regenerated
			// report looks identical to the one before it. That has cost
			// real confusion — the file was right and the screen was
			// stale. The fragment is ignored by the file itself and is
			// enough to make the address new.
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n\nopen in browser:\nfile://%s#%d\n",
				absOut, absOut, time.Now().Unix())
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to report on (default: current directory)")
	cmd.Flags().StringVar(&out, "out", filepath.Join(os.TempDir(), "dun-report.html"), "output file path")
	cmd.Flags().IntVar(&limit, "limit", 500, "number of recent commits to examine")
	cmd.Flags().StringVar(&template, "template", "exec",
		"which report to render: exec, adoption, or detail")
	return cmd
}
