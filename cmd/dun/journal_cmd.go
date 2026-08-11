package main

import (
	"encoding/json"
	"fmt"
	"os"
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
		Short: "Print the full local journal in plain text.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := journalDir()
			if err != nil {
				return err
			}
			entries, err := journal.ReadRange(dir, time.Time{}, time.Time{})
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
		Short: "Delete the entire local journal.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := journalDir()
			if err != nil {
				return err
			}
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("purge journal: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "journal purged")
			return nil
		},
	}
}
