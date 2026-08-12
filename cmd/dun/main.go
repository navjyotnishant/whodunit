// Command dun is the Whodunit CLI: local-only AI-attribution tracking for git repos.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dun:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dun",
		Short: "Track AI-assisted work locally and stamp git commits with attribution.",
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
	return root
}
