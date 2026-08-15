package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// lookDun is stubbed rather than depending on whether a dun happens to be
// installed on the machine running the tests — the same reason verify.go
// made it a variable. Tests that assert about the developer's laptop pass
// locally and fail in CI.
func stubLookDun(t *testing.T, path string, err error) {
	t.Helper()
	prev := lookDun
	lookDun = func() (string, error) { return path, err }
	t.Cleanup(func() { lookDun = prev })
}

// The common case: dun resolves by name, to this binary. Nothing to say,
// and saying something anyway trains people to ignore the output.
func TestReachabilitySaysNothingWhenDunIsOnPath(t *testing.T) {
	self := filepath.Join(t.TempDir(), "dun")
	writeExecutable(t, self)
	stubLookDun(t, self, nil)

	var out bytes.Buffer
	reportReachability(&out, self)

	if out.Len() != 0 {
		t.Errorf("warned about a working install:\n%s", out.String())
	}
}

// Homebrew installs dun as a symlink into the Cellar, so LookPath returns
// the link and os.Executable returns its target. Comparing the raw strings
// warns at every Homebrew user on every `dun init` — a false alarm on the
// most common install route, which is worse than not warning at all.
func TestReachabilityFollowsTheHomebrewSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privilege on Windows")
	}
	dir := t.TempDir()

	cellar := filepath.Join(dir, "Cellar", "dun", "0.2.0", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cellar, "dun")
	writeExecutable(t, target)

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "dun")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// LookPath finds the link; os.Executable would have resolved to the
	// target. This is exactly the pair that must compare equal.
	stubLookDun(t, link, nil)

	var out bytes.Buffer
	reportReachability(&out, target)

	if out.Len() != 0 {
		t.Errorf("warned about a normal Homebrew install:\n%s", out.String())
	}
}

// The failure this exists for: unzip the archive, run init from Downloads,
// tidy up Downloads later. Every commit after that is stamped undetermined,
// silently, and that reads as "no AI was used" (NAV-21).
func TestReachabilityWarnsWhenDunIsNotOnPath(t *testing.T) {
	self := filepath.Join(t.TempDir(), "dun")
	writeExecutable(t, self)
	stubLookDun(t, "", errors.New("not found"))

	var out bytes.Buffer
	reportReachability(&out, self)

	msg := out.String()
	if msg == "" {
		t.Fatal("said nothing about a binary that is not on PATH")
	}
	for _, want := range []string{
		"not on PATH", // the problem
		self,          // what the hooks will fall back to
		"moves or is deleted",
		"undetermined", // what it costs, in the vocabulary of the trailer
		"fix:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q:\n%s", want, msg)
		}
	}
}

// A different dun first on PATH is its own failure, and a confusing one:
// the hooks run that binary, not the one being used to install them, so a
// fix applied here appears to do nothing.
func TestReachabilityWarnsWhenAnotherDunShadowsThisOne(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "build", "dun")
	other := filepath.Join(dir, "usr", "dun")
	writeExecutable(t, self)
	writeExecutable(t, other)

	stubLookDun(t, other, nil)

	var out bytes.Buffer
	reportReachability(&out, self)

	msg := out.String()
	if msg == "" {
		t.Fatal("said nothing about a shadowing binary")
	}
	if !strings.Contains(msg, other) || !strings.Contains(msg, self) {
		t.Errorf("message does not show both paths, so the reader cannot tell them apart:\n%s", msg)
	}
	if !strings.Contains(msg, "different dun") {
		t.Errorf("message does not say the hooks will run a different binary:\n%s", msg)
	}
}

// The fix has to name the platform's actual install route. Telling a
// Windows user to run brew is worse than saying nothing.
func TestReachabilityFixNamesThePlatformsInstaller(t *testing.T) {
	fix := reachabilityFix()
	want := "brew"
	if runtime.GOOS == "windows" {
		want = "scoop"
	}
	if !strings.Contains(fix, want) {
		t.Errorf("fix on %s does not mention %s: %q", runtime.GOOS, want, fix)
	}
}

func TestSameBinary(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "dun")
	writeExecutable(t, a)

	if !sameBinary(a, a) {
		t.Error("a path is not the same as itself")
	}
	if sameBinary(a, filepath.Join(dir, "other")) {
		t.Error("two different paths compared equal")
	}
	// A path that does not exist must still compare, rather than
	// EvalSymlinks failing and the check silently passing.
	missing := filepath.Join(dir, "gone")
	if !sameBinary(missing, missing) {
		t.Error("a missing path is not the same as itself")
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
