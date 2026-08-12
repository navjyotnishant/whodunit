package main

import (
	"fmt"
	"io"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		dsn    string
		limit  int
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Send this repository's attribution data to a shared database.",
		Long: "Sends this repository's commits, agent observations, and line hashes\n" +
			"to a shared database — typically a DevLake instance — so several\n" +
			"repositories or several people's work can be looked at together.\n\n" +
			"This is the only part of whodunit that makes a network call, and it\n" +
			"only runs when you run it. Nothing is sent in the background and\n" +
			"nothing is sent by default.\n\n" +
			"What leaves this machine: commit metadata and trailers, the files an\n" +
			"agent touched and when, and hashes of lines it produced. Not the\n" +
			"lines themselves, not prompts, not file contents.\n\n" +
			"Use --dry-run first to see exactly what would be sent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, dsn, limit, dryRun)
		},
	}

	cmd.Flags().StringVar(&dsn, "to", "", "database url, e.g. mysql://user:pass@host:3306/lake")
	cmd.Flags().IntVar(&limit, "limit", 500, "number of recent commits to include")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be sent without sending it")
	return cmd
}

func runSync(cmd *cobra.Command, dsn string, limit int, dryRun bool) error {
	if dsn == "" && !dryRun {
		return fmt.Errorf("--to is required (or use --dry-run to see what would be sent)")
	}

	payload, err := buildPayload(limit)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if dryRun {
		describePayload(w, payload)
		fmt.Fprintln(w, "\nnothing was sent (--dry-run)")
		return nil
	}

	describePayload(w, payload)

	db, err := sidecar.Open(dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("cannot reach the database: %w", err)
	}
	if err := sidecar.EnsureSchema(db); err != nil {
		return err
	}

	counts, err := sidecar.Write(db, payload)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\nsent %d commit(s), %d event(s), %d line hash(es)\n",
		counts.Commits, counts.Events, counts.Lines)
	return nil
}

// buildPayload assembles everything this repository contributes, from the
// same sources the local report and hooks use.
func buildPayload(limit int) (sidecar.Payload, error) {
	var p sidecar.Payload

	repoID, err := currentRepoID()
	if err != nil {
		return p, err
	}
	dataDir, err := journalDataDir()
	if err != nil {
		return p, err
	}
	now := time.Now().UTC()

	stats, err := report.Collect(limit)
	if err != nil {
		return p, err
	}

	entries, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil {
		return p, err
	}

	lines, err := journal.ReadLineHashes(dataDir, repoID, time.Time{})
	if err != nil {
		return p, err
	}

	// Metadata may be absent for a repository initialised before it
	// existed. That is a state to carry honestly — an empty contributor —
	// rather than a reason to refuse to sync.
	repo := sidecar.RepoRow{RepoID: repoID, SyncedAt: now}
	if md, err := journal.GetMetadata(dataDir, repoID); err == nil && md != nil {
		repo.Contributor = md.Contributor
		repo.SpecVersion = md.SpecVersion
	}

	p.Repo = repo
	p.Commits = sidecar.CommitRowsFrom(stats.Commits, repoID, now)
	p.Events = sidecar.EventRowsFrom(entries, repoID, now)
	p.Lines = sidecar.LineRowsFrom(lines, repoID, now)
	return p, nil
}

// describePayload states what is about to leave the machine, in the terms
// someone would want before agreeing to it.
func describePayload(w io.Writer, p sidecar.Payload) {
	contributor := p.Repo.Contributor
	if contributor == "" {
		contributor = "(none recorded)"
	}

	fmt.Fprintf(w, "repository:   %s\n", p.Repo.RepoID[:min(12, len(p.Repo.RepoID))])
	fmt.Fprintf(w, "contributor:  %s\n", contributor)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-14s %d\n", "commits", len(p.Commits))
	fmt.Fprintf(w, "  %-14s %d\n", "agent events", len(p.Events))
	fmt.Fprintf(w, "  %-14s %d\n", "line hashes", len(p.Lines))

	files := map[string]bool{}
	for _, e := range p.Events {
		if e.File != "" {
			files[e.File] = true
		}
	}
	if len(files) > 0 {
		fmt.Fprintf(w, "  %-14s %d distinct\n", "file paths", len(files))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
