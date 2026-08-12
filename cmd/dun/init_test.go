package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
)

func TestInstallHookPrefersPATHOverBakedInPath(t *testing.T) {
	hooksDir := t.TempDir()

	// The path passed at install time might not exist by the time the hook
	// actually runs (a temp/dev build that got deleted) — the script must
	// not hardcode it as the only way to find dun.
	deadPath := "/does/not/exist/dun"
	if err := installHook(hooksDir, "prepare-commit-msg", deadPath); err != nil {
		t.Fatalf("installHook: %v", err)
	}

	script, err := os.ReadFile(filepath.Join(hooksDir, "prepare-commit-msg"))
	if err != nil {
		t.Fatalf("read installed hook: %v", err)
	}

	if !strings.Contains(string(script), "command -v dun") {
		t.Error("hook script does not attempt PATH resolution before falling back to the baked-in path")
	}
	if !strings.Contains(string(script), deadPath) {
		t.Error("hook script lost the fallback path entirely")
	}
}

func TestInstallHookChainsExistingHook(t *testing.T) {
	hooksDir := t.TempDir()
	existing := "#!/bin/sh\necho pre-existing\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "commit-msg"), []byte(existing), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	if err := installHook(hooksDir, "commit-msg", "/usr/local/bin/dun"); err != nil {
		t.Fatalf("installHook: %v", err)
	}

	chained, err := os.ReadFile(filepath.Join(hooksDir, "commit-msg.dun-chain"))
	if err != nil {
		t.Fatalf("chained hook not preserved: %v", err)
	}
	if string(chained) != existing {
		t.Errorf("chained hook content = %q, want %q", chained, existing)
	}
}

func TestInstallHookIsIdempotent(t *testing.T) {
	hooksDir := t.TempDir()

	if err := installHook(hooksDir, "commit-msg", "/usr/local/bin/dun"); err != nil {
		t.Fatalf("installHook #1: %v", err)
	}
	if err := installHook(hooksDir, "commit-msg", "/usr/local/bin/dun"); err != nil {
		t.Fatalf("installHook #2: %v", err)
	}

	// Re-running init must not chain dun's own hook to itself.
	if _, err := os.Stat(filepath.Join(hooksDir, "commit-msg.dun-chain")); err == nil {
		t.Error("re-running installHook created a chain file pointing at dun's own hook")
	}
}

func TestInitRegistersTheRepository(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	entries, err := registry.List()
	if err != nil {
		t.Fatalf("registry.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 registered repo after init, got %d", len(entries))
	}

	repoID, _ := currentRepoID()
	if entries[0].RepoID != repoID {
		t.Errorf("registered %q, want the current repo %q", entries[0].RepoID, repoID)
	}
}

func TestInitWithRepoFlagInstrumentsElsewhere(t *testing.T) {
	// Instrumenting without cd'ing into the target.
	chdirToTestRepo(t)

	target := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = target
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "seed")

	cmd := newRootCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"init", "--repo", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --repo: %v", err)
	}

	// Hooks must land in the TARGET repo, not the current one.
	if _, err := os.Stat(filepath.Join(target, ".git", "hooks", "prepare-commit-msg")); err != nil {
		t.Errorf("hook not installed in the target repo: %v", err)
	}

	targetID, err := repoid.ForRepo(target)
	if err != nil {
		t.Fatalf("ForRepo: %v", err)
	}
	entries, _ := registry.List()
	found := false
	for _, e := range entries {
		if e.RepoID == targetID {
			found = true
		}
	}
	if !found {
		t.Errorf("target repo not registered; registry has %+v", entries)
	}
}

func TestInitRejectsNonDirectoryRepoFlag(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"init", "--repo", filepath.Join(t.TempDir(), "does-not-exist")})
	if err := cmd.Execute(); err == nil {
		t.Error("init --repo on a missing directory = nil error, want an error")
	}
}

func TestInitOnRepoWithoutCommitsStillInstallsHooks(t *testing.T) {
	// No root commit means no stable identity, so it cannot be registered —
	// but the hooks must still work.
	dir := t.TempDir()
	c := exec.Command("git", "init", "-q")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"init", "--repo", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init on a repo with no commits = %v, want nil", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")); err != nil {
		t.Errorf("hook not installed: %v", err)
	}
	if !strings.Contains(buf.String(), "no commits yet") {
		t.Errorf("output should explain why it was not registered: %s", buf.String())
	}

	entries, _ := registry.List()
	if len(entries) != 0 {
		t.Errorf("a repo with no commits must not be registered, got %+v", entries)
	}
}
