package sidecar

import (
	"database/sql"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
)

func snap(captured time.Time, commits int) *baseline.Snapshot {
	return &baseline.Snapshot{
		SchemaVersion: "1",
		CapturedAt:    captured,
		WindowDays:    90,
		HeadSHA:       "abc123",
		Git: baseline.GitMetrics{
			Commits:          commits,
			CommitsPerWeek:   11.8,
			MedianDiffLines:  107,
			MeanHoursBetween: 14.2,
			Reverts:          0,
			RevertRate:       0,
		},
	}
}

// The baseline reaches the sidecar (NAV-107). Until this landed,
// `dun baseline capture` wrote a snapshot locally and nothing published
// it, so every central before-and-after comparison had no *before*.
func TestBaselineSurvivesTheSync(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	captured := now.Add(-24 * time.Hour)
	row, ok := BaselineRowFrom(snap(captured, 152), "repo-1", now)
	if !ok {
		t.Fatal("BaselineRowFrom refused a real snapshot")
	}
	if _, err := Write(store, Payload{
		Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
		Baseline: &row,
	}); err != nil {
		t.Fatal(err)
	}

	var commits, windowDays int
	var perWeek sql.NullFloat64
	var median sql.NullInt64
	if err := db.QueryRow(`SELECT commits, window_days, commits_per_week,
		median_diff_lines FROM whodunit_baselines WHERE repo_id = 'repo-1'`).
		Scan(&commits, &windowDays, &perWeek, &median); err != nil {
		t.Fatal(err)
	}

	if commits != 152 || windowDays != 90 {
		t.Errorf("commits=%d window=%d, want 152 and 90", commits, windowDays)
	}
	if !perWeek.Valid || perWeek.Float64 < 11.7 || perWeek.Float64 > 11.9 {
		t.Errorf("commits_per_week = %v, want ~11.8", perWeek)
	}
	if !median.Valid || median.Int64 != 107 {
		t.Errorf("median_diff_lines = %v, want 107", median)
	}
}

// A repository that never captured a baseline contributes NOTHING, not a
// row of zeroes.
//
// "No baseline was captured" and "a baseline showing no activity" are
// different claims, and only the second should ever render as zeroes. A
// panel comparing against an all-zero baseline would report enormous
// improvement from nothing (NAV-21).
func TestNoSnapshotProducesNoRow(t *testing.T) {
	if _, ok := BaselineRowFrom(nil, "repo-1", time.Now()); ok {
		t.Error("a nil snapshot produced a row; a repository with no baseline " +
			"would then be compared against zeroes and show infinite improvement")
	}
}

// Re-syncing the same capture must not change the numbers.
//
// A snapshot is immutable by design: baseline.Write refuses to overwrite
// one locally, and the window is fixed BEFORE any comparison exists
// precisely because a window chosen afterwards can manufacture almost any
// figure. The sync has to preserve that guarantee rather than quietly
// replacing the values on every push.
func TestReSyncingABaselineDoesNotChangeIt(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	captured := now.Add(-24 * time.Hour)

	write := func(commits int) {
		t.Helper()
		row, _ := BaselineRowFrom(snap(captured, commits), "repo-1", now)
		if _, err := Write(store, Payload{
			Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
			Baseline: &row,
		}); err != nil {
			t.Fatal(err)
		}
	}

	write(152)
	// The same capture time with different numbers — what a tampered or
	// recomputed local file would look like.
	write(9999)

	var commits int
	if err := db.QueryRow(`SELECT commits FROM whodunit_baselines
		WHERE repo_id = 'repo-1'`).Scan(&commits); err != nil {
		t.Fatal(err)
	}
	if commits != 152 {
		t.Errorf("commits = %d, want 152 — a re-sync overwrote an immutable "+
			"baseline, so the window a comparison was made against is no longer "+
			"the window that was recorded", commits)
	}
}

// A capture with a DIFFERENT timestamp is a new baseline, not a
// replacement — a `--force` recapture, or a different window. Both rows
// are kept, because which one a comparison used is part of the record.
func TestADifferentCaptureIsANewRow(t *testing.T) {
	db := openDB(t)
	store := &Store{DB: db}
	if err := EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for i, captured := range []time.Time{
		now.Add(-48 * time.Hour),
		now.Add(-24 * time.Hour),
	} {
		row, _ := BaselineRowFrom(snap(captured, 100+i), "repo-1", now)
		if _, err := Write(store, Payload{
			Repo:     RepoRow{RepoID: "repo-1", SyncedAt: now},
			Baseline: &row,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM whodunit_baselines
		WHERE repo_id = 'repo-1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("got %d baseline rows, want 2 — a recapture replaced the "+
			"original instead of being recorded beside it", n)
	}
}
