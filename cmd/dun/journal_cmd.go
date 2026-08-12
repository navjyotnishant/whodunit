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
	return &cobra.Command{
		Use:   "show",
		Short: "Print this repository's journal entries in plain text.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := journalDataDir()
			if err != nil {
				return err
			}
			repoID, err := currentRepoID()
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
}

func newJournalPurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Delete this repository's journal entries.",
		Long: "Deletes every journal entry recorded for the current repository.\n\n" +
			"The journal is a single global store shared by every repository, so\n" +
			"this deletes only this repository's rows — other repositories are\n" +
			"left untouched.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := journalDataDir()
			if err != nil {
				return err
			}
			repoID, err := currentRepoID()
			if err != nil {
				return err
			}
			n, err := journal.Purge(dataDir, repoID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %d entr%s for this repository\n", n, plural(n))
			return nil
		},
	}
}

func plural(n int64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
