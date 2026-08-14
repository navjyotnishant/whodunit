package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithAssistedAndPlainCommits(t *testing.T) string {
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
	commit := func(file, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		run("add", file)
		run("commit", "-q", "-m", msg)
	}

	commit("a.go", "feat: assisted work\n\nAI-Attribution: status=assisted; method=observed; agent=claude-code")
	commit("b.go", "feat: manual work")
	return dir
}

func TestDeltaCommandRunsWithoutBaseline(t *testing.T) {
	repoWithAssistedAndPlainCommits(t)

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"delta", "--baseline", filepath.Join(t.TempDir(), "absent.json")})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("delta: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Within-period") {
		t.Errorf("output missing within-period section: %s", out)
	}
	if !strings.Contains(out, "no pre-adoption baseline") {
		t.Errorf("output should say the baseline is missing rather than silently omitting the comparison: %s", out)
	}
	if strings.Contains(out, "Cross-period") {
		t.Errorf("cross-period section must not appear without a baseline: %s", out)
	}
}

func TestDeltaCommandAlwaysShowsRevertRateWithThroughput(t *testing.T) {
	repoWithAssistedAndPlainCommits(t)

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"delta", "--baseline", filepath.Join(t.TempDir(), "absent.json")})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("delta: %v", err)
	}

	out := buf.String()
	// A throughput figure without its revert rate would let a velocity
	// "gain" hide deferred rework.
	if !strings.Contains(out, "commits/week") {
		t.Errorf("missing throughput: %s", out)
	}
	if !strings.Contains(out, "revert rate") {
		t.Errorf("missing revert rate alongside throughput: %s", out)
	}
}

func TestDeltaCommandWithBaselineShowsConfounders(t *testing.T) {
	repoWithAssistedAndPlainCommits(t)
	basePath := filepath.Join(t.TempDir(), "baseline.json")

	capture := newRootCmd()
	capture.SetOut(&strings.Builder{})
	capture.SetArgs([]string{"baseline", "capture", "--out", basePath})
	if err := capture.Execute(); err != nil {
		t.Fatalf("baseline capture: %v", err)
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"delta", "--baseline", basePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delta: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Cross-period") {
		t.Errorf("missing cross-period section: %s", out)
	}
	if !strings.Contains(out, "correlations, not causes") {
		t.Errorf("cross-period output must state it is not causal: %s", out)
	}
}
