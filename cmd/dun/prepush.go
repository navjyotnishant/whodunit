// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The pre-push hook: publish the journal, never block the push.

package main

import (
	"fmt"
	"io"

	"github.com/navjyotnishant/whodunit/internal/config"
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
	if err != nil || !cfg.Sync.Configured() || !cfg.Sync.OnPush {
		return nil
	}

	c := termcolor.New(w)

	if err := syncNow(cfg.Sync, w); err != nil {
		// Named as whodunit's problem, not git's. Without the prefix this
		// reads like a push error and sends someone debugging their remote.
		fmt.Fprintf(w, "%s %v\n", c.S(termcolor.Warn, "whodunit: sync failed:"), err)
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"whodunit: your work is still recorded locally and will sync next time"))
	}
	return nil
}

// syncNow publishes the journal to the configured target.
func syncNow(sync *config.SyncConfig, w io.Writer) error {
	dsn, err := sync.Resolve()
	if err != nil {
		return err
	}

	payload, err := buildPayload(defaultSyncLimit)
	if err != nil {
		return err
	}

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

	counts, err := sidecar.Write(db, payload)
	if err != nil {
		return err
	}

	c := termcolor.New(w)
	fmt.Fprintf(w, "%s %d commit(s), %d event(s), %d session(s)\n",
		c.S(termcolor.Muted, "whodunit: synced"),
		counts.Commits, counts.Events, counts.Sessions)
	return nil
}

// defaultSyncLimit is how many recent commits a push publishes.
//
// The whole journal is sent rather than a delta since the last successful
// sync. The sidecar upserts, so re-sending is harmless, and a watermark is
// state that can drift out of agreement with the target — a resync after a
// database restore would then miss exactly the rows it needed to replace.
// Worth revisiting if this becomes slow; it is bounded by commit count, not
// by history.
const defaultSyncLimit = 500
