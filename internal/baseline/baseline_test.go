package baseline

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/purpose"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
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
	run("init", "-q")

	commit := func(file, content, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
		run("add", file)
		run("commit", "-q", "-m", msg)
	}

	commit("main.go", "package main\nfunc main() {}\n", "feat: add main")
	commit("main_test.go", "package main\n", "test: cover main")
	commit("README.md", "# repo\n", `Revert "feat: add main"`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return dir
}

func TestCaptureComputesGitMetrics(t *testing.T) {
	initRepo(t)

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if snap.Git.Commits != 3 {
		t.Errorf("Commits = %d, want 3", snap.Git.Commits)
	}
	if snap.Git.Reverts != 1 {
		t.Errorf("Reverts = %d, want 1 (the Revert \"...\" commit)", snap.Git.Reverts)
	}
	if snap.Git.RevertRate < 0.32 || snap.Git.RevertRate > 0.34 {
		t.Errorf("RevertRate = %v, want ~0.333 (1 of 3)", snap.Git.RevertRate)
	}
	if snap.Git.MedianDiffLines <= 0 {
		t.Errorf("MedianDiffLines = %d, want > 0", snap.Git.MedianDiffLines)
	}
	if snap.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", snap.SchemaVersion, SchemaVersion)
	}
	if snap.HeadSHA == "" {
		t.Error("HeadSHA is empty")
	}
	if snap.Git.PurposeDistribution[purpose.Feature] != 1 {
		t.Errorf("purpose feature = %d, want 1", snap.Git.PurposeDistribution[purpose.Feature])
	}
	if snap.Git.PurposeDistribution[purpose.Test] != 1 {
		t.Errorf("purpose test = %d, want 1", snap.Git.PurposeDistribution[purpose.Test])
	}
}

func TestCaptureOmitsManualWhenNotSupplied(t *testing.T) {
	initRepo(t)

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Manual != nil {
		t.Errorf("Manual = %+v, want nil when nothing was supplied", snap.Manual)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "\"manual\"") {
		t.Error("unsupplied manual metrics must be omitted entirely, not serialized as zero values")
	}
}

func TestCaptureRecordsManualMetrics(t *testing.T) {
	initRepo(t)

	prs := 42
	cycle := 6.5
	manual := &ManualMetrics{PRsMerged: &prs, MedianCycleTimeHrs: &cycle, Note: "from GitHub Insights"}

	snap, err := Capture(90, manual, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Manual == nil || snap.Manual.PRsMerged == nil || *snap.Manual.PRsMerged != 42 {
		t.Errorf("Manual.PRsMerged not recorded: %+v", snap.Manual)
	}
	if snap.Manual.ChangeFailureRate != nil {
		t.Error("ChangeFailureRate should stay nil when not supplied, not default to 0")
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	// A baseline measures a window that cannot be recaptured — silently
	// replacing it destroys the only copy.
	if err := Write(path, snap); err == nil {
		t.Error("second Write() = nil error, want refusal to overwrite an existing baseline")
	}
}

func TestWriteProducesReadableJSON(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var round Snapshot
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if round.Git.Commits != snap.Git.Commits {
		t.Errorf("round-tripped commits = %d, want %d", round.Git.Commits, snap.Git.Commits)
	}
}

func TestCaptureOnEmptyRepoErrors(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	// Unlike report/status, a baseline over zero commits is meaningless
	// rather than a valid empty result — surface it rather than writing a
	// snapshot that measures nothing.
	if _, err := Capture(90, nil, time.Now()); err == nil {
		t.Error("Capture() on an empty repo = nil error, want an error")
	}
}
