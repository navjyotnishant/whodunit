package gitexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A git that never returns is stopped at Limit.
func TestOutputStopsAtLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil { // #nosec G306 -- test stand-in
		t.Fatal(err)
	}
	old := Limit
	Limit = 200 * time.Millisecond
	defer func() { Limit = old }()
	start := time.Now()
	if _, err := Output(fake, dir, "rev-parse", "--show-toplevel"); err == nil || time.Since(start) > 8*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

// A command that finishes in time is unchanged.
func TestOutputRunsGit(t *testing.T) {
	out, err := Output("git", t.TempDir(), "--version")
	if err != nil || len(out) == 0 {
		t.Fatalf("git --version: %q %v", out, err)
	}
}
