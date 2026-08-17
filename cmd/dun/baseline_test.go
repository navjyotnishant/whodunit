package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repoWithOneCommit(t *testing.T) string {
	t.Helper()
	dir := chdirToTestRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "main.go")
	run("commit", "-q", "-m", "feat: initial")
	return dir
}

func TestBaselineCaptureWritesSnapshot(t *testing.T) {
	repoWithOneCommit(t)
	out := filepath.Join(t.TempDir(), "baseline.json")

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"baseline", "capture", "--days", "90", "--out", out})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("baseline capture: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if snap["schema_version"] == nil {
		t.Error("snapshot missing schema_version")
	}
	if !strings.Contains(buf.String(), "captured baseline") {
		t.Errorf("output missing summary: %s", buf.String())
	}
}

func TestBaselineCaptureRecordsManualFlags(t *testing.T) {
	repoWithOneCommit(t)
	out := filepath.Join(t.TempDir(), "baseline.json")

	cmd := newRootCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"baseline", "capture", "--days", "90", "--out", out, "--prs-merged", "17", "--note", "hand-entered"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("baseline capture: %v", err)
	}

	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), `"prs_merged": 17`) {
		t.Errorf("manual PR count not recorded: %s", data)
	}
	if strings.Contains(string(data), "change_failure_rate") {
		t.Error("unsupplied manual metric must be omitted, not written as zero")
	}
}

func TestBaselineCaptureRefusesSecondRun(t *testing.T) {
	repoWithOneCommit(t)
	out := filepath.Join(t.TempDir(), "baseline.json")

	first := newRootCmd()
	first.SetOut(&strings.Builder{})
	first.SetArgs([]string{"baseline", "capture", "--days", "90", "--out", out})
	if err := first.Execute(); err != nil {
		t.Fatalf("first capture: %v", err)
	}

	second := newRootCmd()
	second.SetOut(&strings.Builder{})
	second.SetErr(&strings.Builder{})
	second.SetArgs([]string{"baseline", "capture", "--days", "90", "--out", out})
	if err := second.Execute(); err == nil {
		t.Error("second capture = nil error, want refusal to overwrite an immutable baseline")
	}
}

// The whole point of --since/--until: the user names the period they
// worked without an agent, and that is what gets measured.
func TestResolveWindowHonoursNamedRange(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	w, explicit, err := resolveWindow("2026-01-01", "2026-06-30", 90, now)
	if err != nil {
		t.Fatalf("resolveWindow: %v", err)
	}
	if !explicit {
		t.Error("a named range must report itself as explicit")
	}
	if got := w.Since.Format("2006-01-02"); got != "2026-01-01" {
		t.Errorf("Since = %s, want 2026-01-01", got)
	}
	// Exclusive end: the day after the one named, so that day counts.
	if got := w.Until.Format("2006-01-02"); got != "2026-07-01" {
		t.Errorf("Until = %s, want 2026-07-01 (the named day must be included)", got)
	}
}

func TestResolveWindowDefaultsToDays(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	w, explicit, err := resolveWindow("", "", 90, now)
	if err != nil {
		t.Fatalf("resolveWindow: %v", err)
	}
	if explicit {
		t.Error("no flags given, so the window is not explicit")
	}
	if w.Days() != 90 {
		t.Errorf("Days() = %d, want 90", w.Days())
	}
	if !w.Until.Equal(now) {
		t.Errorf("Until = %s, want now", w.Until)
	}
}

// --since alone must still end now, not 90 days after the start.
func TestResolveWindowSinceAloneEndsNow(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	w, _, err := resolveWindow("2026-01-01", "", 90, now)
	if err != nil {
		t.Fatalf("resolveWindow: %v", err)
	}
	if !w.Until.Equal(now) {
		t.Errorf("Until = %s, want now (%s)", w.Until, now)
	}
}

func TestResolveWindowRejectsBadDates(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct{ since, until string }{
		{"01-01-2026", ""},
		{"", "June 30"},
		{"2026-13-45", ""},
	} {
		if _, _, err := resolveWindow(tc.since, tc.until, 90, now); err == nil {
			t.Errorf("resolveWindow(%q, %q) accepted a malformed date", tc.since, tc.until)
		}
	}
}

// A bare `dun baseline capture` prints help rather than capturing.
//
// The old no-argument path measured 90 days ending today, which stops being
// pre-adoption once hooks are installed — so it silently produced the one
// baseline nobody wants: AI-assisted work recorded as the before.
func TestBaselineCaptureWithNoWindowPrintsHelp(t *testing.T) {
	dir := repoWithOneCommit(t)
	out := filepath.Join(dir, "baseline.json")

	cmd := newRootCmd()
	var buf strings.Builder
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"baseline", "capture", "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare capture should print help, not fail: %v", err)
	}

	if _, err := os.Stat(out); err == nil {
		t.Error("bare capture wrote a snapshot; it must capture nothing without an explicit window")
	}
	if got := buf.String(); !strings.Contains(got, "--since") {
		t.Errorf("help does not mention --since:\n%s", got)
	}
}
