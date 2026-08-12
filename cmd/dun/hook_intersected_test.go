// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The commit hook must reach intersected on a fresh install,
// with no prior `dun ingest`.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// NAV-72 criterion 3, and the reason the issue exists.
//
// determineTrailer used to parse transcripts, compute line hashes, discard
// them, and then read those hashes back from a journal that only `dun
// ingest` ever wrote. On a machine where nobody ran that command by hand —
// which is every normal install — the journal was empty and attribution was
// capped at observed forever, while the hook held the exact evidence it
// needed.
func TestFreshInstallReachesIntersected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home) // an empty journal: nothing ingested, ever
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")

	// Resolve the repository path the way the adapter will: on macOS a temp
	// directory is reached through /var, which is a symlink to /private/var,
	// and Claude Code records the resolved form.
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	// The agent writes a file, exactly as a transcript would record it.
	const body = "package main\n\nfunc addedByAgent() int {\n\treturn 42\n}\n"
	target := filepath.Join(realRepo, "agent.go")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)

	// The same content is staged, unmodified — the case intersected exists
	// to recognise.
	git(t, repo, "add", "-A")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	trailer := determineTrailer()

	if trailer.Method != spec.MethodIntersected {
		t.Fatalf("method = %q, want %q.\n"+
			"A fresh install with no prior `dun ingest` must still reach "+
			"intersected: the hook parses the line hashes it needs and must "+
			"not throw them away.\ntrailer: %s",
			trailer.Method, spec.MethodIntersected, trailer.Format())
	}
	if trailer.Status != spec.StatusAssisted {
		t.Errorf("status = %q, want assisted", trailer.Status)
	}
}

// The hook writes what it read, so a later report or sync sees it without
// anyone running `dun ingest`.
func TestHookPopulatesTheJournal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	const body = "const x = 1\n"
	target := filepath.Join(realRepo, "x.js")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)
	git(t, repo, "add", "-A")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	_ = determineTrailer()

	// The journal is the shared surface: dun report, dun sync and the
	// dashboards all read it. If the hook does not write, all three are
	// empty on a normal install.
	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	repoID, err := currentRepoID()
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := journal.ReadLineHashes(dataDir, repoID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) == 0 {
		t.Fatal("the journal is empty after a commit hook ran; nothing " +
			"downstream (report, sync, dashboards) will see this work")
	}
}

// A journal that cannot be written must not fail the commit. The trailer
// degrades to whatever the transcripts alone support (NAV-21).
func TestUnwritableJournalStillStampsATrailer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	const body = "let y = 2\n"
	target := filepath.Join(realRepo, "y.js")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)
	git(t, repo, "add", "-A")

	// A file where the data directory should be: every journal write fails.
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Dir(dataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	trailer := determineTrailer() // must not panic, must return something

	// Still assisted: the transcripts prove an agent touched the staged
	// file even though nothing could be recorded.
	if trailer.Status != spec.StatusAssisted {
		t.Fatalf("status = %q with an unwritable journal, want assisted "+
			"from the transcripts alone", trailer.Status)
	}

	// And still intersected, which is the point of using the hashes the
	// hook just parsed rather than only what the journal holds. An earlier
	// version of this test asserted the opposite — that a failed write
	// should cost the upgrade — but that would mean a machine with a broken
	// journal under-reports work it can see and prove in memory. Persisting
	// is for everything downstream; the trailer only needs the evidence.
	if trailer.Method != spec.MethodIntersected {
		t.Errorf("method = %q with an unwritable journal, want intersected: "+
			"the hashes were parsed in this process and do not depend on the "+
			"write succeeding", trailer.Method)
	}
}

// writeTranscriptFor writes a Claude Code transcript recording one Write of
// body into target, in the layout the adapter reads.
func writeTranscriptFor(tb testing.TB, repo, target, body string) {
	tb.Helper()

	claudeHome := tb.TempDir()
	tb.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	dir := filepath.Join(claudeHome, "projects", claudecode.SlugForCwd(repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}

	f, err := os.Create(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	now := time.Now().UTC()
	enc := json.NewEncoder(f)

	if err := enc.Encode(map[string]any{
		"type": "assistant", "timestamp": now, "sessionId": "test-session",
		"version": "2.0.0",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_use", "name": "Write", "id": "toolu_1",
			"input": map[string]any{"file_path": target, "content": body},
		}}},
	}); err != nil {
		tb.Fatal(err)
	}
	if err := enc.Encode(map[string]any{
		"type": "user", "timestamp": now.Add(time.Second), "sessionId": "test-session",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok",
		}}},
	}); err != nil {
		tb.Fatal(err)
	}
}

// NAV-7: the raw session id is the transcript filename. A commit carrying
// it points at a file holding every prompt of that session, permanently, in
// a message that gets pushed.
//
// Asserted at the hook rather than only on SessionToken: the derivation can
// be correct while the hook still stamps the raw value.
func TestTrailerCarriesAHashedSessionNotTheRawID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	const body = "const z = 3\n"
	target := filepath.Join(realRepo, "z.js")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFor(t, realRepo, target, body)
	git(t, repo, "add", "-A")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	trailer := determineTrailer()

	// writeTranscriptFor records sessionId "test-session".
	if trailer.Session == "test-session" {
		t.Fatalf("the trailer carries the agent's raw session id:\n%s", trailer.Format())
	}
	if trailer.Session == "" {
		t.Fatal("the trailer carries no session at all; grouping is lost")
	}
	if len(trailer.Session) != spec.SessionTokenLength {
		t.Fatalf("session = %q, want a %d-char token", trailer.Session, spec.SessionTokenLength)
	}
}
