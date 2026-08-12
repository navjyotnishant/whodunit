package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Parsing is on the commit path: the prepare-commit-msg hook reads
// transcripts to decide what trailer to stamp, on every single commit. A
// change that makes it quadratic would show up as commits getting slower,
// which is the thing a developer uninstalls a tool over.
//
// These benchmarks scale the input rather than asserting a wall-clock
// number. A budget measured on one laptop says nothing about a machine with
// ten times the history; growth that stays linear does.
func BenchmarkParseSince(b *testing.B) {
	for _, events := range []int{100, 1000, 10000} {
		path := writeBenchTranscript(b, events)
		b.Run(fmt.Sprintf("events=%d", events), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				entries, err := ParseSince(path, time.Time{})
				if err != nil {
					b.Fatal(err)
				}
				if len(entries) == 0 {
					b.Fatal("parsed nothing; the benchmark is measuring the wrong thing")
				}
			}
		})
	}
}

// The `since` cutoff is what makes repeated ingest cheap — a daemon tick
// re-reads the same transcript and should skip almost all of it. If this is
// not much faster than a full parse, incremental ingest is not incremental.
func BenchmarkParseSinceSkipsOldEvents(b *testing.B) {
	path := writeBenchTranscript(b, 10000)
	future := time.Now().Add(24 * time.Hour)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entries, err := ParseSince(path, future)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != 0 {
			b.Fatalf("cutoff in the future still returned %d entries", len(entries))
		}
	}
}

func BenchmarkParseSessionActivity(b *testing.B) {
	path := writeBenchTranscript(b, 10000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ParseSessionActivity(path, time.Time{}); err != nil {
			b.Fatal(err)
		}
	}
}

// writeBenchTranscript builds a transcript with n tool_use events, in the
// shape Claude Code actually writes: a call and its result as separate
// records, joined by tool_use_id.
func writeBenchTranscript(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	path := filepath.Join(dir, "bench.jsonl")

	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := "func f() {\n\treturn 1\n}\n"

	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("toolu_%d", i)

		if err := enc.Encode(map[string]any{
			"type": "assistant", "timestamp": ts, "sessionId": "bench", "version": "1.0.0",
			"message": map[string]any{"content": []any{map[string]any{
				"type": "tool_use", "name": "Write", "id": id,
				"input": map[string]any{
					"file_path": fmt.Sprintf("/repo/file%d.go", i%50),
					"content":   body,
				},
			}}},
		}); err != nil {
			tb.Fatal(err)
		}

		if err := enc.Encode(map[string]any{
			"type": "user", "timestamp": ts.Add(time.Millisecond), "sessionId": "bench",
			"message": map[string]any{"content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": id, "content": "ok",
			}}},
		}); err != nil {
			tb.Fatal(err)
		}
	}
	return path
}
