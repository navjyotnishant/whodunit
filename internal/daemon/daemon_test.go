package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunCallsIngestOnStartAndStopsOnCancel(t *testing.T) {
	dir := t.TempDir() // does not contain a real Claude Code session dir

	var calls int32
	ingest := func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, ingest, func(string) {}) }()

	// Run() must call ingest at least once immediately on start, before any
	// tick or watch event — that's what makes `dun daemon run` useful the
	// instant it starts rather than only after the first poll interval.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		select {
		case <-deadline:
			t.Fatal("ingest was never called on daemon start")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() returned %v after cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestRunSurvivesMissingSessionDir(t *testing.T) {
	// cwd whose Claude Code session directory doesn't exist yet — the
	// common case for a fresh repo or before any session has started.
	// Run must not error or block; it degrades to polling until the
	// directory appears (verified by the daemon_test above never blocking).
	dir := t.TempDir()

	ingest := func() (int, error) { return 0, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := Run(ctx, dir+"/does/not/exist", ingest, func(string) {}); err != nil {
		t.Errorf("Run() with missing session dir = %v, want nil", err)
	}
}
