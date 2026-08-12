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
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a self-contained local HTML report (no server, no network).",
		RunE: func(cmd *cobra.Command, args []string) error {
			stats, err := report.Collect(limit)
			if err != nil {
				return err
			}
			var b strings.Builder
			report.Render(&b, stats)
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
	return cmd
}
