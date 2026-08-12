package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SessionFiles is the expensive one, and it runs on the commit path.
//
// Codex files sessions by date rather than by repository, so the only way
// to know which repository a session belongs to is to open it. That cost
// grows with every session on the machine, not with the size of the
// repository being committed to — so a developer who uses Codex heavily
// pays it on every commit, in every repository.
//
// The benchmark scales the number of transcripts to show what that curve
// looks like.
func BenchmarkSessionFiles(b *testing.B) {
	for _, transcripts := range []int{10, 100, 1000} {
		root := writeBenchSessions(b, transcripts)
		b.Run(fmt.Sprintf("transcripts=%d", transcripts), func(b *testing.B) {
			b.Setenv("WHODUNIT_CODEX_PATH", root)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				files, err := SessionFiles("/repo/target")
				if err != nil {
					b.Fatal(err)
				}
				if len(files) == 0 {
					b.Fatal("matched nothing; the benchmark is measuring the wrong thing")
				}
			}
		})
	}
}

func BenchmarkParseSince(b *testing.B) {
	for _, patches := range []int{100, 1000} {
		path := writeBenchRollout(b, patches)
		b.Run(fmt.Sprintf("patches=%d", patches), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				entries, err := ParseSince(path, time.Time{})
				if err != nil {
					b.Fatal(err)
				}
				if len(entries) == 0 {
					b.Fatal("parsed nothing")
				}
			}
		})
	}
}

// A transcript older than the cutoff must not be read at all. Without the
// check, collecting outcomes reads the whole file before the cutoff is
// consulted, and a no-op ingest costs more than a real one.
func BenchmarkParseSinceSkipsOldTranscripts(b *testing.B) {
	path := writeBenchRollout(b, 1000)
	future := time.Now().Add(24 * time.Hour)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entries, err := ParseSince(path, future)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != 0 {
			b.Fatalf("cutoff in the future returned %d entries", len(entries))
		}
	}
}

// writeBenchSessions lays out n rollout transcripts the way Codex does,
// only one of which belongs to the repository being searched for.
func writeBenchSessions(tb testing.TB, n int) string {
	tb.Helper()
	root := tb.TempDir()
	day := filepath.Join(root, "2026", "08", "12")
	if err := os.MkdirAll(day, 0o755); err != nil {
		tb.Fatal(err)
	}

	for i := 0; i < n; i++ {
		cwd := fmt.Sprintf("/repo/other%d", i)
		if i == n/2 {
			cwd = "/repo/target"
		}
		path := filepath.Join(day, fmt.Sprintf("rollout-%04d.jsonl", i))
		writeRollout(tb, path, cwd, 5)
	}
	return root
}

func writeBenchRollout(tb testing.TB, patches int) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "rollout.jsonl")
	writeRollout(tb, path, "/repo/target", patches)
	return path
}

func writeRollout(tb testing.TB, path, cwd string, patches int) {
	tb.Helper()
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	base := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	if err := enc.Encode(map[string]any{
		"timestamp": base, "type": "session_meta",
		"payload": map[string]any{"id": "bench", "cwd": cwd, "cli_version": "1.0.0"},
	}); err != nil {
		tb.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: main.go\n@@\n-old line\n+new line\n+another new line\n*** End Patch"
	for i := 0; i < patches; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("call_%d", i)

		if err := enc.Encode(map[string]any{
			"timestamp": ts, "type": "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "name": "apply_patch",
				"call_id": id, "input": patch,
			},
		}); err != nil {
			tb.Fatal(err)
		}
		if err := enc.Encode(map[string]any{
			"timestamp": ts, "type": "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": id,
				"output": "Success. Updated the following files:\nM main.go\n",
			},
		}); err != nil {
			tb.Fatal(err)
		}
	}
}
