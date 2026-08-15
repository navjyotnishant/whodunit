package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// instrumented returns a repository with whodunit's hooks installed, which
// is the state every test here starts from — newRepo alone gives a bare
// repository, and repairing one of those is a different case (it has no
// hooks because it was never instrumented, not because they went stale).
func instrumented(t *testing.T, name string) string {
	t.Helper()
	repo := newRepo(t, name)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(repo, ".git", "hooks")
	for _, h := range trackedHooks {
		if err := installHook(hooks, h, self); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// A hook added after a repository was instrumented is installed on the
// next `dun` command there (NAV-76).
//
// This is the failure the ticket exists for: pre-push was added after
// some repositories were already set up, so those never synced —
// silently, with nothing to indicate anything was missing. It was found
// only because someone noticed an unrelated 0% coverage figure.
func TestSelfRepairInstallsAHookAddedLater(t *testing.T) {
	repo := instrumented(t, "stale")
	hooks := filepath.Join(repo, ".git", "hooks")

	// The state a repository instrumented before pre-push existed is in.
	if err := os.Remove(filepath.Join(hooks, "pre-push")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	repairHooks(&out, termcolor.New(&out), repo)

	if _, err := os.Stat(filepath.Join(hooks, "pre-push")); err != nil {
		t.Fatalf("pre-push was not installed: %v", err)
	}
	if !strings.Contains(out.String(), "hook") {
		t.Errorf("repaired silently; the reader has no idea it happened: %q",
			out.String())
	}
}

// Nothing is said when the hooks are already current.
//
// A notice on every command is noise that trains people to skip the
// output, which is how the next real message gets missed (criterion 7).
func TestSelfRepairIsSilentWhenCurrent(t *testing.T) {
	repo := instrumented(t, "current")

	var out bytes.Buffer
	repairHooks(&out, termcolor.New(&out), repo)

	if out.Len() != 0 {
		t.Errorf("said something about hooks that were already fine: %q", out.String())
	}
}

// A pre-existing hook that whodunit did not write survives the repair.
//
// This is what makes an unattended repair safe at all (criterion 9). An
// automatic fix that ate someone's husky or lefthook hook would be far
// worse than the staleness it set out to correct.
func TestSelfRepairPreservesSomeoneElsesHook(t *testing.T) {
	repo := instrumented(t, "chained")
	hooks := filepath.Join(repo, ".git", "hooks")

	// Another tool installs over one of whodunit's hooks, and a different
	// hook goes missing so a repair is actually triggered — a foreign hook
	// alone does not read as stale, since staleHooks only asks whether the
	// file exists and which version wrote it.
	foreign := "#!/bin/sh\necho 'someone elses hook'\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(hooks, "commit-msg")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	repairHooks(&out, termcolor.New(&out), repo)

	chain, err := os.ReadFile(filepath.Join(hooks, "pre-push.dun-chain"))
	if err != nil {
		t.Fatalf("the foreign hook was not preserved: %v", err)
	}
	if !strings.Contains(string(chain), "someone elses hook") {
		t.Errorf("chained hook does not hold the original: %q", chain)
	}
}

// A directory that is not a repository is left alone rather than erroring.
// `dun status` outside a repository is a legitimate call.
func TestSelfRepairIgnoresANonRepository(t *testing.T) {
	var out bytes.Buffer
	repairHooks(&out, termcolor.New(&out), t.TempDir())

	if out.Len() != 0 {
		t.Errorf("said something about a directory with no git: %q", out.String())
	}
}
