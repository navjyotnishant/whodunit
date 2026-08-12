package main

import (
	"fmt"
	"os"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/spf13/cobra"
)

func newIngestCmd() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Read local AI agent session transcripts into the journal.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(cmd, since)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only ingest events at or after this RFC3339 timestamp (default: all)")
	return cmd
}

func runIngest(cmd *cobra.Command, sinceFlag string) error {
	var since time.Time
	if sinceFlag != "" {
		t, err := time.Parse(time.RFC3339, sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since: %w", err)
		}
		since = t
	}

	written, sessionCount, err := ingestSince(since, func(path string, err error) {
		fmt.Fprintf(cmd.ErrOrStderr(), "skip %s: %v\n", path, err)
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "ingested %d event(s) from %d session file(s)\n", written, sessionCount)
	return nil
}

// runIngestCount runs a full ingest and returns just the written count, for
// the daemon loop — same underlying logic as `dun ingest`, no --since bound.
func runIngestCount() (int, error) {
	written, _, err := ingestSince(time.Time{}, func(string, error) {})
	return written, err
}

// ingestSince reads every Claude Code session for the current repo at or
// after since and writes new events into the journal. Duplicate events are
// silently skipped at the journal layer (Append is idempotent), so calling
// this repeatedly — from a CLI run or a daemon loop — is always safe.
func ingestSince(since time.Time, onSkip func(path string, err error)) (written, sessionCount int, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 0, 0, err
	}
	// Every registered agent, not one named here: an agent added later is
	// ingested without this function changing. A failure to look for one
	// agent's sessions must not stop the others, so it is reported through
	// onSkip and the loop continues — otherwise a single misconfigured
	// path would silence every agent on the machine.
	type sessionFile struct {
		path string
		a    adapter.Adapter
	}
	var sessionFiles []sessionFile
	for _, ad := range adapter.All() {
		paths, err := ad.SessionFiles(cwd)
		if err != nil {
			onSkip(ad.Name(), err)
			continue
		}
		for _, p := range paths {
			sessionFiles = append(sessionFiles, sessionFile{path: p, a: ad})
		}
	}

	dataDir, err := journalDataDir()
	if err != nil {
		return 0, 0, err
	}
	repoID, err := currentRepoID()
	if err != nil {
		return 0, 0, err
	}
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		return 0, 0, err
	}
	defer w.Close()

	for _, sf := range sessionFiles {
		p := sf.path
		entries, err := sf.a.ParseSince(p, since)
		if err != nil {
			onSkip(p, err)
			continue
		}
		// Session engagement is per session, not per tool call, so it is
		// summarised separately (NAV-55).
		if acts, err := sf.a.ParseSessionActivity(p, since); err == nil {
			for _, a := range acts {
				if err := w.UpsertSession(a); err != nil {
					return written, len(sessionFiles), fmt.Errorf("write session: %w", err)
				}
			}
		}

		for _, e := range entries {
			if err := w.Append(e); err != nil {
				return written, len(sessionFiles), fmt.Errorf("write journal entry: %w", err)
			}
			if err := w.AppendLines(e.LineHashes, e.Timestamp); err != nil {
				return written, len(sessionFiles), fmt.Errorf("write line hashes: %w", err)
			}
			written++
		}
	}

	return written, len(sessionFiles), nil
}
