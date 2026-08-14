package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	cmd.SetArgs([]string{"baseline", "capture", "--out", out})

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
	cmd.SetArgs([]string{"baseline", "capture", "--out", out, "--prs-merged", "17", "--note", "hand-entered"})

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
	first.SetArgs([]string{"baseline", "capture", "--out", out})
	if err := first.Execute(); err != nil {
		t.Fatalf("first capture: %v", err)
	}

	second := newRootCmd()
	second.SetOut(&strings.Builder{})
	second.SetErr(&strings.Builder{})
	second.SetArgs([]string{"baseline", "capture", "--out", out})
	if err := second.Execute(); err == nil {
		t.Error("second capture = nil error, want refusal to overwrite an immutable baseline")
	}
}
