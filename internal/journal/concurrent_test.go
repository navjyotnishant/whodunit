// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Concurrent writers, which is the normal configuration here.

package journal

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Concurrent writers are the default configuration, not an edge case.
//
// The journal is one file per machine, shared by every repository, and three
// things write to it: the prepare-commit-msg hook on every commit, the
// daemon on a five-second timer, and `dun ingest` by hand. A developer with
// two repositories open and the daemon running has three writers against one
// file.
//
// SQLite's default rollback-journal mode fails a concurrent writer
// immediately with SQLITE_BUSY rather than waiting. Every caller here treats
// a journal error as "record nothing and carry on" — which is correct, and
// means a collision costs a silently unattributed commit rather than a
// visible failure. That is the NAV-21 mask again: the commit is stamped
// undetermined and reads as "no AI was used".
func TestConcurrentWritersDoNotLoseEntries(t *testing.T) {
	dataDir := t.TempDir()

	const (
		writers          = 4
		entriesPerWriter = 50
	)

	var wg sync.WaitGroup
	errs := make(chan error, writers*entriesPerWriter)
	now := time.Now().UTC()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each goroutine opens its own writer, as separate processes
			// would: this is not testing one *sql.DB's internal pooling, it
			// is testing several connections to one file.
			jw, err := NewWriter(dataDir, testRepo)
			if err != nil {
				errs <- fmt.Errorf("writer %d: %w", w, err)
				return
			}
			defer jw.Close()

			for i := 0; i < entriesPerWriter; i++ {
				ts := now.Add(-time.Duration(w*entriesPerWriter+i) * time.Minute)
				if err := jw.Append(Entry{
					Timestamp: ts,
					Agent:     "claude-code",
					Session:   fmt.Sprintf("sess-%d", w),
					Event:     "tool_use",
					Tool:      "Edit",
					File:      fmt.Sprintf("/repo/w%d/file%d.go", w, i),
				}); err != nil {
					errs <- fmt.Errorf("writer %d entry %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errs)

	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d writes failed under concurrency; the first was: %v\n\n"+
			"Every caller treats a journal error as 'record nothing and carry "+
			"on', so each of these is a silently unattributed commit.",
			len(failures), writers*entriesPerWriter, failures[0])
	}

	got, err := ReadRange(dataDir, testRepo, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if want := writers * entriesPerWriter; len(got) != want {
		t.Errorf("recorded %d entries, want %d: writes were accepted but did "+
			"not all land", len(got), want)
	}
}

// The line-hash table is written in its own transaction, so it collides
// independently of the entries table — and it is the one the commit hook
// needs for intersected attribution.
func TestConcurrentLineHashWritesDoNotLoseHashes(t *testing.T) {
	dataDir := t.TempDir()

	const (
		writers         = 4
		hashesPerWriter = 50
	)

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	now := time.Now().UTC()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			jw, err := NewWriter(dataDir, testRepo)
			if err != nil {
				errs <- err
				return
			}
			defer jw.Close()

			for i := 0; i < hashesPerWriter; i++ {
				h := uint64(w)<<32 | uint64(i)
				if err := jw.AppendLines([]uint64{h}, now); err != nil {
					errs <- fmt.Errorf("writer %d hash %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("a concurrent line-hash write failed: %v\n\n"+
			"agentLineHashes returns nil on error, so the commit being made "+
			"at that moment silently degrades from intersected to observed.", err)
	}

	hashes, err := ReadLineHashes(dataDir, testRepo, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if want := writers * hashesPerWriter; len(hashes) != want {
		t.Errorf("recorded %d hashes, want %d", len(hashes), want)
	}
}

// A reader must not fail because a writer holds the file. This is the exact
// commit-time collision: the hook reads line hashes to decide a trailer
// while the daemon is mid-ingest.
func TestReadingWhileWritingSucceeds(t *testing.T) {
	dataDir := t.TempDir()
	seedManyEntries(t, dataDir, 200)

	stop := make(chan struct{})
	writeErr := make(chan error, 1)

	go func() {
		jw, err := NewWriter(dataDir, "busy-writer-repo")
		if err != nil {
			writeErr <- err
			return
		}
		defer jw.Close()

		now := time.Now().UTC()
		for i := 0; ; i++ {
			select {
			case <-stop:
				writeErr <- nil
				return
			default:
			}
			ts := now.Add(-time.Duration(i) * time.Second)
			if err := jw.Append(Entry{
				Timestamp: ts, Agent: "claude-code", Session: "busy",
				Event: "tool_use", Tool: "Edit",
				File: fmt.Sprintf("/busy/f%d.go", i),
			}); err != nil {
				writeErr <- err
				return
			}
		}
	}()

	// Read repeatedly while that writer hammers the same file.
	var readFailures int
	for i := 0; i < 30; i++ {
		if _, err := ReadLineHashes(dataDir, testRepo, time.Time{}); err != nil {
			readFailures++
			t.Logf("read %d failed: %v", i, err)
		}
	}
	close(stop)

	if err := <-writeErr; err != nil {
		t.Fatalf("the background writer failed: %v", err)
	}
	if readFailures > 0 {
		t.Fatalf("%d of 30 reads failed while another process was writing.\n\n"+
			"determineTrailer falls back to an empty hash set when this read "+
			"fails, so every commit made during a daemon tick loses its "+
			"intersected attribution with nothing logged to explain it.",
			readFailures)
	}
}
