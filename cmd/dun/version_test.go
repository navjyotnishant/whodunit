// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Hook version stamping and staleness detection.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/registry"
)

// A hook has to record which version wrote it, or nothing can tell an
// outdated hook set from a current one — which is how a repository ends up
// missing a hook added months earlier, silently.
func TestInstalledHookRecordsItsVersion(t *testing.T) {
	dir := t.TempDir()
	if err := installHook(dir, "pre-push", "/usr/local/bin/dun"); err != nil {
		t.Fatal(err)
	}

	got, ok := hookVersionOf(filepath.Join(dir, "pre-push"))
	if !ok {
		t.Fatal("the installed hook carries no version stamp")
	}
	if got != version {
		t.Fatalf("hook records version %q, binary is %q", got, version)
	}
}

// A hook written before stamping existed has no marker. That absence is
// itself the signal: it predates the mechanism, so it is stale.
func TestUnstampedHookReadsAsStale(t *testing.T) {
	dir := t.TempDir()
	hooks := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}

	// The shape dun wrote before versions were recorded.
	old := "#!/bin/sh\n" + hookMarker + "\nDUN=\"$(command -v dun)\"\n\"$DUN\" hook pre-push \"$@\"\n"
	for _, name := range trackedHooks {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(old), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := hookVersionOf(filepath.Join(hooks, "pre-push")); ok {
		t.Fatal("an unstamped hook reported a version")
	}

	missing, stale := staleHooks(dir)
	if len(missing) != 0 {
		t.Fatalf("hooks exist but were reported missing: %v", missing)
	}
	// A dev build reports nothing stale: it has no version worth comparing
	// against, and flagging hooks as outdated relative to a local build is
	// noise rather than a finding.
	if IsRelease() && len(stale) != len(trackedHooks) {
		t.Fatalf("a release build saw %d stale hooks, want %d", len(stale), len(trackedHooks))
	}
	if !IsRelease() && len(stale) != 0 {
		t.Fatalf("a dev build reported %d stale hooks; it has no version to compare", len(stale))
	}
}

func TestMissingHooksAreReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	missing, _ := staleHooks(dir)
	if len(missing) != len(trackedHooks) {
		t.Fatalf("got %d missing, want all %d", len(missing), len(trackedHooks))
	}
}

// Updating hooks must not eat a hook whodunit did not write. An automatic
// repair that deletes someone's husky hook is worse than staleness.
func TestUpdatePreservesAForeignHook(t *testing.T) {
	dir := t.TempDir()
	foreign := "#!/bin/sh\necho 'someone else's hook'\n"
	if err := os.WriteFile(filepath.Join(dir, "pre-push"), []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := installHook(dir, "pre-push", "/usr/local/bin/dun"); err != nil {
		t.Fatal(err)
	}

	chained, err := os.ReadFile(filepath.Join(dir, "pre-push.dun-chain"))
	if err != nil {
		t.Fatalf("the pre-existing hook was not preserved: %v", err)
	}
	if !strings.Contains(string(chained), "someone else's hook") {
		t.Fatalf("the preserved hook is not the original: %q", chained)
	}
}

// `dun update` runs from scripts and CI as readily as from a terminal. A
// confirmation prompt with nothing to read from blocks forever, which is
// worse than refusing.
func TestUpdateDoesNotBlockWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	if err := runUpdate(&out, &bytes.Buffer{}, false); err != nil {
		t.Fatalf("runUpdate returned %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "terminal") && !strings.Contains(s, "not installed by Homebrew") {
		t.Fatalf("update neither refused nor explained itself:\n%s", s)
	}
}

// Repositories that have moved are skipped rather than failing the run —
// one absent directory must not stop every later repository from updating.
func TestReposUpdateSkipsMissingRepositories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	gone := filepath.Join(t.TempDir(), "gone-away")
	if err := registry.Add("repo-gone", gone, time.Now()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runReposUpdate(&out); err != nil {
		t.Fatalf("a missing repository failed the whole update: %v", err)
	}
	if !strings.Contains(out.String(), "moved or deleted") {
		t.Fatalf("the missing repository was not reported:\n%s", out.String())
	}
}
