// Package daemon watches Claude Code session transcripts for this repo and
// re-ingests them into the journal as they change. v1 is foreground-only —
// no OS service install (that's NAV-31, deferred) — just a watch loop
// callers run however they like (a terminal, tmux, a systemd unit someone
// writes by hand).
package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
)

// pollInterval is the fallback cadence when fsnotify isn't watching
// (unavailable, or the directory doesn't exist yet). fsnotify is unreliable
// on network mounts and in some container setups (NAV-32) — polling is the
// floor everything else sits on top of, not a last resort.
const pollInterval = 5 * time.Second

// IngestFunc performs one ingest pass. Injected so the daemon loop doesn't
// import cmd/dun and stays testable without touching a real journal/transcript.
type IngestFunc func() (ingested int, err error)

// Run watches cwd's Claude Code session directory and calls ingest whenever
// a session file changes, plus unconditionally every pollInterval as a
// floor. Blocks until ctx is canceled.
func Run(ctx context.Context, cwd string, ingest IngestFunc, log func(string)) error {
	dir := claudecode.SessionDir(cwd)

	watcher, watchErr := fsnotify.NewWatcher()
	watching := false
	if watchErr != nil {
		log(fmt.Sprintf("fsnotify unavailable, falling back to polling only: %v", watchErr))
		watcher = nil
	} else {
		defer watcher.Close()
		if err := watcher.Add(dir); err != nil {
			// Common case: the session directory doesn't exist yet (no
			// Claude Code session has started here). The poll ticker below
			// retries this every interval, so watching picks up as soon as
			// the directory appears — this isn't a permanent fallback.
			log(fmt.Sprintf("not watching %s yet (%v); polling until it appears", dir, err))
		} else {
			watching = true
		}
	}

	runOnce := func() {
		n, err := ingest()
		if err != nil {
			log(fmt.Sprintf("ingest error: %v", err))
			return
		}
		if n > 0 {
			log(fmt.Sprintf("ingested %d event(s)", n))
		}
	}

	runOnce()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var events <-chan fsnotify.Event
	var errs <-chan error
	if watcher != nil {
		events = watcher.Events
		errs = watcher.Errors
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if watcher != nil && !watching {
				if err := watcher.Add(dir); err == nil {
					watching = true
					log(fmt.Sprintf("now watching %s", dir))
				}
			}
			runOnce()
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			runOnce()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			log(fmt.Sprintf("watcher error: %v", err))
		}
	}
}
