package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/spf13/cobra"
)

func newReportCmd() *cobra.Command {
	var out string
	var limit int
	var template string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a self-contained local HTML report (no server, no network).",
		Long: "Renders a single HTML file with no external requests: it opens\n" +
			"offline, behind a firewall, and as an email attachment.\n\n" +
			"Three templates answer different questions:\n\n" +
			"  exec      is adoption growing, and is the work landing (default)\n" +
			"  adoption  who and what is being used — agents, tools, sessions\n" +
			"  detail    what exactly happened, per commit and per file",
		RunE: func(cmd *cobra.Command, args []string) error {
			tmpl, err := report.ParseTemplate(template)
			if err != nil {
				return err
			}

			stats, err := report.Collect(limit)
			if err != nil {
				return err
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
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n\nopen in browser:\nfile://%s\n", absOut, absOut)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", filepath.Join(os.TempDir(), "dun-report.html"), "output file path")
	cmd.Flags().IntVar(&limit, "limit", 500, "number of recent commits to examine")
	cmd.Flags().StringVar(&template, "template", "exec",
		"which report to render: exec, adoption, or detail")
	return cmd
}
