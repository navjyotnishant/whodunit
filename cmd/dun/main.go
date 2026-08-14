// Command dun is the Whodunit CLI: local-only AI-attribution tracking for git repos.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	// Cobra prints the error itself, prefixed "Error:". Printing it again
	// here gave every failure twice — once from cobra, once as "dun: …" —
	// which reads as two problems and buries multi-line advice under a
	// repeat of its own first line.
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dun",
		Short: "Track AI-assisted work locally and stamp git commits with attribution.",
		// Bare `dun` shows where you are and what to run next, rather
		// than a list of twelve commands with no indication of which
		// one applies. `dun --help` still gives the full surface.
		//
		// SilenceUsage keeps a runtime error from printing the whole
		// usage block after it, which buries the error itself.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q — run 'dun --help'", args[0])
			}
			return runWelcome(cmd.OutOrStdout(), cmd)
		},
		SilenceUsage: true,
	}
	root.AddCommand(newInitCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newJournalCmd())
	root.AddCommand(newHookCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newIngestCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newBaselineCmd())
	root.AddCommand(newDeltaCmd())
	root.AddCommand(newReposCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newVerifyCmd())
	root.AddCommand(newLogCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newUpdateCmd())
	return root
}
