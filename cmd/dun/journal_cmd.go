package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/spf13/cobra"
)

func newJournalCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "journal",
		Short: "Inspect or manage the local observation journal.",
	}
	root.AddCommand(newJournalShowCmd())
	root.AddCommand(newJournalPurgeCmd())
	return root
}

func newJournalShowCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print a repository's journal entries in plain text.",
		Long: "Prints journal entries as one JSON object per line.\n\n" +
			"Defaults to the repository in the current directory. Use --repo to\n" +
			"inspect another instrumented repository from anywhere; it accepts a\n" +
			"path or a repo id as printed by `dun repos list`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := journalDataDir()
			if err != nil {
				return err
			}
			repoID, _, err := resolveRepo(repoFlag)
			if err != nil {
				return err
			}
			entries, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, e := range entries {
				if err := enc.Encode(e); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path or repo id to inspect (default: current directory)")
	return cmd
}

func newJournalPurgeCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete a repository's journal entries.",
		Long: "Deletes every journal entry recorded for a repository.\n\n" +
			"The journal is a single global store shared by every repository, so\n" +
			"this deletes only the target repository's rows — other repositories\n" +
			"are left untouched.\n\n" +
			"Defaults to the repository in the current directory. With --repo it\n" +
			"names the target before deleting, because purging the wrong\n" +
			"repository cannot be undone.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := journalDataDir()
			if err != nil {
				return err
			}
			repoID, label, err := resolveRepo(repoFlag)
			if err != nil {
				return err
			}

			// Say what is about to be lost, before losing it. With a
			// global store and a --repo flag, a typo deletes another
			// project's history and there is no undo — so the target is
			// named up front rather than only in the past tense after.
			out := cmd.OutOrStdout()
			if repoFlag != "" {
				fmt.Fprintf(out, "purging journal entries for %s\n", label)
			}

			n, err := journal.Purge(dataDir, repoID)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "purged %d entr%s for %s\n", n, plural(n), label)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path or repo id to purge (default: current directory)")
	return cmd
}

func plural(n int64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
