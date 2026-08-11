package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
