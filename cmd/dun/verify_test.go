// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: dun verify — health reporting, exit codes, and no side effects.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/registry"
)

// NAV-77 criterion 6, and the one that decides whether this command is
// useful or ignored. Local-only is a supported way to run whodunit — the
// setup wizard offers it — so an unconfigured sync must be a fact, not a
// failure, and must not fail CI.
func TestUnconfiguredSyncIsNotAFailure(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	// Real but empty directories: "installed, not used here" rather than
	// a configured path that does not exist, which verify correctly
	// reports as broken.
	t.Setenv("WHODUNIT_CODEX_PATH", t.TempDir())
	t.Setenv("WHODUNIT_AGY_PATH", t.TempDir())

	repo := newRepo(t, "local-only")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	// Hooks present, so the only unconfigured thing is sync.
	if err := runInit(newStatusTestCmd(&bytes.Buffer{}), ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runVerify(&out, "")
	s := out.String()

	if !strings.Contains(s, "not configured") {
		t.Fatalf("verify did not report the unconfigured sync:\n%s", s)
	}
	if err != nil {
		t.Fatalf("an unconfigured sync failed the command (%v); local-only is "+
			"supported and must not break a CI gate:\n%s", err, s)
	}
}

// Criterion 2: a problem without its remedy has done half the job.
func TestBrokenConfigNamesItsFix(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nowhere")
	t.Setenv("WHODUNIT_CODEX_PATH", missing)

	repo := newRepo(t, "broken")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runVerify(&out, "")
	s := out.String()

	if !strings.Contains(s, missing) {
		t.Errorf("the broken path was not named:\n%s", s)
	}
	if !strings.Contains(s, "dun config set agent.codex.path") {
		t.Errorf("the fix command was not given:\n%s", s)
	}
	if err == nil {
		t.Error("a configured-but-missing path did not fail the command")
	}
}

// Criterion 3: someone fixing a setup wants the whole list, not one item
// per run.
func TestAllProblemsAreReportedNotJustTheFirst(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "nowhere-1"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "nowhere-2"))

	// A repository with no hooks at all, so hooks are broken too.
	repo := newRepo(t, "several-problems")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	_ = runVerify(&out, "")
	s := out.String()

	if strings.Count(s, "!!") < 2 {
		t.Fatalf("verify stopped at the first problem; expected several:\n%s", s)
	}
}

// Criterion 8. The obvious implementation calls determineTrailer, which
// ingests into the journal as a side effect. Someone debugging runs verify
// repeatedly, and a health check must not change what it measures.
func TestVerifyWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	// Real but empty directories: "installed, not used here" rather than
	// a configured path that does not exist, which verify correctly
	// reports as broken.
	t.Setenv("WHODUNIT_CODEX_PATH", t.TempDir())
	t.Setenv("WHODUNIT_AGY_PATH", t.TempDir())

	repo := newRepo(t, "untouched")
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	// An agent transcript exists, so there is something verify could
	// ingest if it were careless.
	const body = "const a = 1\n"
	target := filepath.Join(realRepo, "a.js")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	repoID, err := currentRepoID()
	if err != nil {
		t.Fatal(err)
	}

	before, _ := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})

	var out bytes.Buffer
	_ = runVerify(&out, "")

	after, _ := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if len(after) != len(before) {
		t.Fatalf("verify wrote to the journal: %d events before, %d after. "+
			"A health check must not change what it is measuring.",
			len(before), len(after))
	}
}

// Criterion 5: outside a repository it still reports the machine and the
// registered repositories, rather than failing.
func TestVerifyOutsideARepository(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	// Real but empty directories: "installed, not used here" rather than
	// a configured path that does not exist, which verify correctly
	// reports as broken.
	t.Setenv("WHODUNIT_CODEX_PATH", t.TempDir())
	t.Setenv("WHODUNIT_AGY_PATH", t.TempDir())

	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runVerify(&out, ""); err != nil {
		t.Fatalf("verify failed outside a repository: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "repositories") {
		t.Fatalf("verify did not report registered repositories:\n%s", out.String())
	}
}

// Criterion 1: a healthy setup says so and exits zero.
func TestHealthySetupExitsZero(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	// Real but empty directories: "installed, not used here" rather than
	// a configured path that does not exist, which verify correctly
	// reports as broken.
	t.Setenv("WHODUNIT_CODEX_PATH", t.TempDir())
	t.Setenv("WHODUNIT_AGY_PATH", t.TempDir())

	repo := newRepo(t, "healthy")
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	const body = "const b = 2\n"
	target := filepath.Join(realRepo, "b.js")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	if err := runInit(newStatusTestCmd(&bytes.Buffer{}), ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runVerify(&out, ""); err != nil {
		t.Fatalf("a healthy setup reported a problem: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "attribution is working") {
		t.Fatalf("a healthy setup did not say so:\n%s", out.String())
	}
}

// Criterion 10: recency, not just presence. A journal that stopped growing
// is the symptom shared by nearly every silent failure this tool has.
func TestJournalReportsWhenItLastGrew(t *testing.T) {
	if got := humanAge(time.Now().Add(-2 * time.Hour)); !strings.Contains(got, "hour") {
		t.Errorf("humanAge(2h) = %q", got)
	}
	if got := humanAge(time.Now().Add(-72 * time.Hour)); !strings.Contains(got, "day") {
		t.Errorf("humanAge(3d) = %q", got)
	}
	if got := humanAge(time.Now().Add(-10 * time.Minute)); !strings.Contains(got, "less than") {
		t.Errorf("humanAge(10m) = %q", got)
	}
}

// Run outside a repository, verify must check each registered repository
// rather than only counting them. "3 instrumented" says nothing about
// whether any of them works, and this is the one view that reaches a
// repository nobody has visited in months.
func TestVerifyChecksEachRegisteredRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_CODEX_PATH", t.TempDir())
	t.Setenv("WHODUNIT_AGY_PATH", t.TempDir())

	// One instrumented repository, and one registered without hooks.
	good := newRepo(t, "instrumented")
	bare := newRepo(t, "no-hooks")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)

	if err := os.Chdir(good); err != nil {
		t.Fatal(err)
	}
	if err := runInit(newStatusTestCmd(&bytes.Buffer{}), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(bare); err != nil {
		t.Fatal(err)
	}
	// Registered, but never given hooks — the state a repository is left
	// in when a new hook is added after it was instrumented.
	id, _, err := resolveRepo("")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Add(id, bare, time.Now()); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runVerify(&out, "")
	s := out.String()

	if !strings.Contains(s, "no-hooks") {
		t.Fatalf("the repository with missing hooks was not named:\n%s", s)
	}
	if !strings.Contains(s, "dun init --repo") {
		t.Fatalf("no per-repository fix was offered:\n%s", s)
	}
	if err == nil {
		t.Fatalf("a repository with missing hooks did not fail the command:\n%s", s)
	}
}
