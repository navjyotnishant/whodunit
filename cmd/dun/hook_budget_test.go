// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Budget test for the commit hook, which runs on every commit.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
)

// hookBudget is what determining a trailer may cost.
//
// The prepare-commit-msg hook runs on every commit, so this is the one path
// where slow is not merely annoying — it is the reason a developer removes
// the tool. The budget is deliberately loose: it exists to catch a
// regression of the wrong order (a full re-parse per commit, an accidental
// quadratic), not to police a few milliseconds.
//
// Wall-clock on a shared CI runner is noisy, which is why the number is set
// well above anything a correct implementation produces rather than close
// to the measured value. A gate that flakes gets disabled, and a disabled
// test is worse than none because it still implies protection.
const hookBudget = 2 * time.Second

// A hook that has to scan a machine's worth of agent history still has to
// be fast, because that history belongs to other repositories and grows
// without bound while this repository stays small.
func TestHookStaysWithinBudget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	// A plausible amount of unrelated history: 200 Claude Code transcripts
	// for other repositories, and 200 Codex sessions likewise. None of it
	// belongs to the repository being committed to, so all of it is cost
	// with no benefit — which is exactly the case worth bounding.
	t.Setenv("CLAUDE_CONFIG_DIR", writeClaudeHistory(t, 200))
	t.Setenv("WHODUNIT_CODEX_PATH", writeCodexHistory(t, 200))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_ = determineTrailer()
	elapsed := time.Since(start)

	if elapsed > hookBudget {
		t.Fatalf("determineTrailer took %v, over the %v budget, with 400 unrelated "+
			"sessions on the machine. This runs on every commit.", elapsed, hookBudget)
	}
	t.Logf("determineTrailer: %v (budget %v)", elapsed, hookBudget)
}

// Ingest is idempotent, but idempotent is not the same as cheap. The daemon
// re-runs it on a timer, so a second pass that finds nothing new must cost
// materially less than the first — otherwise every tick pays full price to
// discover there is no work.
func TestRepeatIngestIsCheaperThanTheFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", writeClaudeHistory(t, 50))
	t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
	t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	// Measured against the adapter rather than through ingestSince: session
	// discovery walks the same directories either way and would dominate
	// the comparison, hiding the very regression this is guarding. An
	// earlier version of this test did exactly that and still passed with
	// the cutoff check removed — a test that cannot fail is worse than
	// none, because it implies protection that is not there.
	transcripts, err := claudecode.SessionFiles("/repo/other0")
	if err != nil || len(transcripts) == 0 {
		t.Fatalf("no transcripts to measure: %v", err)
	}
	path := transcripts[0]

	// Repeat both measurements: a single timing on a loaded machine is
	// noise, and the ratio is what matters.
	const rounds = 20
	start := time.Now()
	for i := 0; i < rounds; i++ {
		if _, err := claudecode.ParseSince(path, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	full := time.Since(start)

	future := time.Now().Add(24 * time.Hour)
	start = time.Now()
	for i := 0; i < rounds; i++ {
		entries, err := claudecode.ParseSince(path, future)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("a cutoff in the future returned %d entries", len(entries))
		}
	}
	noop := time.Since(start)

	// A no-op has to be substantially cheaper, not merely not-slower.
	// Without the cutoff check it costs *more* than the real parse, since
	// it pays for the outcome pass and then discards every event.
	if noop*4 > full {
		t.Fatalf("a no-op parse (%v) is not meaningfully cheaper than a full one (%v); "+
			"the cutoff is being applied after the file is already read", noop, full)
	}
	t.Logf("full parse %v, no-op parse %v over %d rounds", full, noop, rounds)
}

func writeClaudeHistory(tb testing.TB, sessions int) string {
	tb.Helper()
	root := tb.TempDir()
	projects := filepath.Join(root, "projects")

	for i := 0; i < sessions; i++ {
		dir := filepath.Join(projects, fmt.Sprintf("-repo-other%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "session.jsonl"))
		if err != nil {
			tb.Fatal(err)
		}
		enc := json.NewEncoder(f)
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for j := 0; j < 20; j++ {
			_ = enc.Encode(map[string]any{
				"type": "assistant", "timestamp": base.Add(time.Duration(j) * time.Second),
				"sessionId": "s", "version": "1.0.0",
				"message": map[string]any{"content": []any{map[string]any{
					"type": "tool_use", "name": "Write", "id": fmt.Sprintf("t%d", j),
					"input": map[string]any{"file_path": "/repo/x.go", "content": "line\n"},
				}}},
			})
		}
		f.Close()
	}
	return root
}

func writeCodexHistory(tb testing.TB, sessions int) string {
	tb.Helper()
	root := tb.TempDir()
	day := filepath.Join(root, "2026", "08", "12")
	if err := os.MkdirAll(day, 0o755); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < sessions; i++ {
		f, err := os.Create(filepath.Join(day, fmt.Sprintf("rollout-%04d.jsonl", i)))
		if err != nil {
			tb.Fatal(err)
		}
		enc := json.NewEncoder(f)
		_ = enc.Encode(map[string]any{
			"timestamp": time.Now(), "type": "session_meta",
			"payload": map[string]any{
				"id": "s", "cwd": fmt.Sprintf("/repo/other%d", i), "cli_version": "1",
			},
		})
		f.Close()
	}
	return root
}
