package main

import (
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/report"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		repoFlag string
		dsn      string
		limit    int
		dryRun   bool
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
			return runSync(cmd, dsn, limit, dryRun, repoFlag)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to publish (default: current directory)")
	cmd.Flags().StringVar(&dsn, "to", "", "database url, e.g. mysql://user:pass@host:3306/lake")
	cmd.Flags().IntVar(&limit, "limit", 500, "number of recent commits to include")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be sent without sending it")
	return cmd
}

func runSync(cmd *cobra.Command, dsn string, limit int, dryRun bool, repoFlag string) error {
	restore, err := enterRepo(repoFlag, "sync", "publish")
	if err != nil {
		return err
	}
	defer restore()

	// Fall back to the configured target, which is what the pre-push hook
	// already uses. Without this the hook syncs on push while the command
	// that exists to sync refuses to, and the advice it gave — pass --to —
	// meant putting a password in a command line and therefore in shell
	// history, undoing the point of storing it encrypted (NAV-80).
	if dsn == "" && !dryRun {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.Sync.Configured() {
			return fmt.Errorf("no sync target: run `dun config datalake`, " +
				"or pass --to (or --dry-run to see what would be sent)")
		}
		resolved, err := cfg.Sync.Resolve()
		if err != nil {
			return err
		}
		dsn = resolved
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

	// Progress for the steps that wait on a network, not for the whole
	// command. Sending a few thousand rows takes long enough that silence
	// reads as a hang, and the failure that matters — an unreachable
	// database — happens in these three steps rather than in the local
	// work above.
	c := termcolor.New(w)
	step := func(msg string) { fmt.Fprintf(w, "%s %s... ", c.S(termcolor.Muted, "→"), msg) }
	ok := func() { fmt.Fprintf(w, "%s\n", c.S(termcolor.Good, "ok")) }
	failed := func() { fmt.Fprintf(w, "%s\n", c.S(termcolor.Bad, "failed")) }

	fmt.Fprintln(w)
	step("connecting")
	db, err := sidecar.Open(dsn)
	if err != nil {
		failed()
		logHook("sync", hooklog.LevelWarn, "sync", "could not open the target: "+err.Error())
		return err
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		failed()
		logHook("sync", hooklog.LevelWarn, "sync", "target unreachable: "+err.Error())
		return fmt.Errorf("cannot reach the database: %w", err)
	}
	ok()

	step("checking the schema")
	if err := sidecar.EnsureSchema(db); err != nil {
		failed()
		logHook("sync", hooklog.LevelWarn, "sync", "schema check failed: "+err.Error())
		return err
	}
	ok()

	bar := newProgressBar(w, "sending")
	counts, err := sidecar.WriteProgress(db, payload, bar.Update)
	bar.Done()
	if err != nil {
		step("sending")
		failed()
		logHook("sync", hooklog.LevelWarn, "sync", "write failed: "+err.Error())
		return err
	}

	fmt.Fprintf(w, "\nsent %d commit(s), %d event(s), %d session(s), %d line hash(es)\n",
		counts.Commits, counts.Events, counts.Sessions, counts.Lines)

	// The same housekeeping the pre-push hook does, for the same reason:
	// the data has just reached a second place, which is the only condition
	// under which removing it locally is safe. Running it here too means
	// `dun sync` and a push behave identically rather than one of them
	// quietly skipping the copy.
	logHook("sync", hooklog.LevelInfo, "sync",
		fmt.Sprintf("published %d commit(s), %d event(s), %d session(s), %d line hash(es) to %s",
			counts.Commits, counts.Events, counts.Sessions, counts.Lines,
			redactedTarget(dsn)))

	afterSuccessfulSync("sync")
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

	sessions, err := journal.ReadSessions(dataDir, repoID)
	if err != nil {
		return p, err
	}

	// Metadata may be absent for a repository initialised before it
	// existed, or captured before git had an identity configured.
	repo := sidecar.RepoRow{RepoID: repoID, SyncedAt: now}
	if md, err := journal.GetMetadata(dataDir, repoID); err == nil && md != nil {
		repo.Contributor = md.Contributor
		repo.SpecVersion = md.SpecVersion
	}

	// Recover the contributor from git when the journal has none, and
	// record it so the next sync does not have to (NAV-110).
	//
	// Previously an absent contributor was carried through as an empty
	// string on the grounds that it was the honest state. It is honest and
	// it is also unusable: every dashboard filters sessions by joining
	// this column, an empty value matches no filter, and the variable's
	// own query excludes empty strings so it is not even selectable.
	// Measured before this fix, 115 of 131 sessions belonged to a
	// repository in that state — synced, and invisible.
	//
	// The identity is sitting in git config the whole time. Reading it
	// here costs one subprocess per sync and closes the gap for every
	// repository instrumented before SetMetadata existed.
	if repo.Contributor == "" {
		if c := contributorFor(""); c != "" {
			repo.Contributor = c
			// Best-effort: a sync that cannot write metadata should still
			// publish, and the value above is already correct for this run.
			_ = journal.SetMetadata(dataDir, journal.Metadata{
				RepoID:      repoID,
				Contributor: c,
				SpecVersion: repo.SpecVersion,
				UpdatedAt:   now,
			})
		}
	}

	p.Repo = repo
	p.Commits = sidecar.CommitRowsFrom(stats.Commits, repoID, now)
	p.Events = sidecar.EventRowsFrom(entries, repoID, now)
	p.Lines = sidecar.LineRowsFrom(lines, repoID, now)
	p.Sessions = sidecar.SessionRowsFrom(sessions, repoID, now)

	// The pre-adoption baseline, when one was captured (NAV-107).
	//
	// This is the comparison the delivery dashboards actually want: the
	// same repository before and after, rather than assisted against
	// unassisted commits in the same period — which is a weaker question,
	// because an agent is reached for on some kinds of work and not
	// others.
	//
	// A repository without one contributes nothing rather than an empty
	// row: "no baseline was captured" and "a baseline showing no activity"
	// are different, and only the second should ever render as zeroes.
	if path, err := baselinePathFor(""); err == nil {
		if snap, err := baseline.Load(path); err == nil {
			if row, ok := sidecar.BaselineRowFrom(snap, repoID, now); ok {
				p.Baseline = &row
			}
		}
	}

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
	fmt.Fprintf(w, "  %-14s %d\n", "sessions", len(p.Sessions))
	fmt.Fprintf(w, "  %-14s %d\n", "line hashes", len(p.Lines))

	// Named explicitly rather than folded into a count. A baseline is a
	// summary of the repository BEFORE instrumentation, which is a
	// different kind of thing from the observations above it, and someone
	// deciding whether to publish should see it listed.
	if b := p.Baseline; b != nil {
		fmt.Fprintf(w, "  %-14s %d commits over %d days, captured %s\n",
			"baseline", b.Commits, b.WindowDays,
			b.CapturedAt.Format("2006-01-02"))
	}

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

// redactedTarget renders a DSN safely for the log.
//
// The log is read by people and pasted into issues, and a resolved DSN
// carries the sync password. config.SyncConfig.Redacted does this for a
// configured target; --to supplies a raw string that has never been through
// it.
func redactedTarget(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "the configured target"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.String()
}
