// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-11-23
// Description: The pre-push hook: publish the journal, never block the push.

package main

import (
	"fmt"
	"io"

	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// runPrePush publishes this repository's journal to the configured target.
//
// It returns nil in every case, including every failure. A push that fails
// is a blocked deploy, and an analytics sidecar that can block a deploy is
// a tool people remove. The journal keeps the data either way, and the next
// push retries — nothing is lost by giving up here.
//
// Silence is the other half of that contract. A machine with no target
// configured prints nothing at all: a hook that emits a line on every push
// for a feature nobody asked for is how people learn to ignore its output,
// including the warning below that they would actually want to read.
func runPrePush(w io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		// An unreadable config means sync silently never runs. Printing
		// would break the silence contract above, so it goes to the log —
		// which is the difference between "sync is off" and "sync is
		// broken", indistinguishable at the terminal either way.
		logHook(hookPrePush, hooklog.LevelWarn, "sync",
			"cannot read the config, so nothing was published: "+err.Error())
		return nil
	}
	if !cfg.Sync.Configured() || !cfg.Sync.OnPush {
		return nil
	}

	c := termcolor.New(w)

	if err := syncNow(cfg.Sync, w); err != nil {
		// Named as whodunit's problem, not git's. Without the prefix this
		// reads like a push error and sends someone debugging their remote.
		fmt.Fprintf(w, "%s %v\n", c.S(termcolor.Warn, "whodunit: sync failed:"), err)
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"whodunit: your work is still recorded locally and will sync next time"))

		// Logged as well as printed. The terminal line scrolls past during
		// a push and is gone; "did the push sync, and what did it send" is
		// asked later, when only the log is left.
		logHook(hookPrePush, hooklog.LevelWarn, "sync", err.Error())
	}
	return nil
}

// syncNow publishes the journal to the configured target.
func syncNow(sync *config.SyncConfig, w io.Writer) error {
	dsn, err := sync.Resolve()
	if err != nil {
		return err
	}

	repoID, err := currentRepoID()
	if err != nil {
		return err
	}
	start := time.Now().UTC()
	since := syncSince(repoID, dsn)

	db, err := sidecar.Open(dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return err
	}
	if err := sidecar.EnsureSchema(db); err != nil {
		return err
	}

	// Same rule as `dun sync`: a lake with nothing for this repository
	// gets everything, whatever the local mark says.
	if !since.IsZero() {
		if last, err := sidecar.LastSync(db, repoID); err == nil && last.IsZero() {
			since = time.Time{}
		}
	}
	payload, err := buildPayload(defaultSyncLimit, since)
	if err != nil {
		return err
	}

	counts, err := sidecar.Write(db, payload)
	if err != nil {
		return err
	}
	recordSync(repoID, dsn, start, hookPrePush)

	c := termcolor.New(w)
	fmt.Fprintf(w, "%s %d commit(s), %d event(s), %d session(s)\n",
		c.S(termcolor.Muted, "whodunit: synced"),
		counts.Commits, counts.Events, counts.Sessions)

	logHook(hookPrePush, hooklog.LevelInfo, "sync",
		fmt.Sprintf("published %d commit(s), %d event(s), %d session(s)",
			counts.Commits, counts.Events, counts.Sessions))

	afterSuccessfulSync(hookPrePush)
	return nil
}

// afterSuccessfulSync takes a backup and prunes what has now been published.
//
// Ordering is the whole safety argument. Sync sends the entire journal and
// the sidecar upserts on the same keys, so anything pruned after a successful
// write already exists in a second place — no watermark to keep, and no
// query to the remote asking what it has.
//
// Both halves are best-effort and neither can fail a push. A backup that
// failed leaves the previous one; a prune that failed leaves a larger file.
// Blocking someone's push over disk hygiene would be the worse trade, and
// `dun log` records what happened either way.
func afterSuccessfulSync(hook string) {
	home, err := config.Dir()
	if err != nil {
		return
	}
	dataDir, err := journalDataDir()
	if err != nil {
		return
	}

	// Backup first. Pruning removes rows the copy should still contain, so
	// taking the copy afterwards would bake the prune into the only local
	// history of it.
	cfg, err := config.Load()
	if err != nil {
		return
	}

	if taken, err := journal.Backup(home, dataDir, cfg.BackupDays); err != nil {
		logHook(hook, hooklog.LevelWarn, "backup", err.Error())
	} else if taken {
		logHook(hook, hooklog.LevelInfo, "backup", "wrote a daily copy of the journal")
	}

	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
	deleted, vacuumed, err := journal.Prune(dataDir, cutoff)
	if err != nil {
		logHook(hook, hooklog.LevelWarn, "prune", err.Error())
		return
	}
	if deleted > 0 {
		detail := fmt.Sprintf("removed %d line hash(es) older than %d days",
			deleted, cfg.RetentionDays)
		if vacuumed {
			detail += ", and reclaimed the space"
		}
		logHook(hook, hooklog.LevelInfo, "prune", detail)
	}
}

// defaultSyncLimit is how many recent commits a push publishes.
//
// Commits always go in full, up to this limit. Events, sessions and line
// hashes go only when changed since the last successful sync to the same
// target (syncSince). Sending the whole journal every time was tried first
// and measured at 29,496 rows a push on one machine, nearly all unchanged.
// The mark can disagree with a restored lake; `dun sync --full` covers
// that, and a lake with no rows for the repository gets everything anyway.
const defaultSyncLimit = 500
