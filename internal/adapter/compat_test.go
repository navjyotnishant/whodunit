package adapter_test

// Compatibility suite (ADR 0008 in whodunit-pro): what each adapter reads out
// of an agent's transcript is the evidence every trailer and every synced row
// is built from. This golden pins it, line hashes included, before capture is
// moved behind a public package (spec 002, task T001), so the move can be
// proven to change nothing.
//
//	WHODUNIT_UPDATE_COMPAT=1 go test ./internal/adapter -run Compat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/compattest"

	_ "github.com/navjyotnishant/whodunit/internal/adapter/agy"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
)

// fixtures maps each adapter to the transcripts in its testdata that it
// parses directly. agy's toolcalls.db is a sidecar read through
// conversation.db, not a transcript of its own.
var fixtures = map[string][]string{
	"agy":         {"agy/testdata/conversation.db"},
	"claude-code": globT("claudecode/testdata/*.jsonl"),
	"codex":       {"codex/testdata/rollout-basic.jsonl"},
}

func globT(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Strings(m)
	return m
}

// fixedTime replaces the fixture's modification time. agy takes an edit's
// timestamp from its database file's mtime, which a checkout sets to "now", so
// without this the golden would differ on every machine.
var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// pinnedCopy copies a fixture into a temporary directory under the same file
// name (session ids derive from it) and gives it a fixed mtime.
func pinnedCopy(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dst, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestCompatGoldenAdapterOutput(t *testing.T) {
	var b strings.Builder
	names := []string{}
	for _, a := range adapter.All() {
		names = append(names, a.Name())
	}
	fmt.Fprintf(&b, "adapters: %s\n", strings.Join(names, ", "))

	for _, a := range adapter.All() {
		paths := fixtures[a.Name()]
		if len(paths) == 0 {
			t.Fatalf("no fixtures listed for adapter %q; add them so its output is pinned", a.Name())
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("fixture %s: %v", p, err)
			}
			fmt.Fprintf(&b, "\n== %s %s\n", a.Name(), filepath.ToSlash(p))
			p = pinnedCopy(t, p)

			entries, err := a.ParseSince(p, time.Time{})
			if err != nil {
				fmt.Fprintf(&b, "ParseSince error: %v\n", err)
			}
			for _, e := range entries {
				row, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				b.Write(row)
				b.WriteString("\n")
				// LineHashes are not serialised (json:"-") and are the part
				// attribution actually matches on, so they are written out.
				if len(e.LineHashes) > 0 {
					hs := make([]string, len(e.LineHashes))
					for i, h := range e.LineHashes {
						hs[i] = fmt.Sprintf("%016x", h)
					}
					fmt.Fprintf(&b, "  line_hashes: %s\n", strings.Join(hs, " "))
				}
			}

			sessions, err := a.ParseSessionActivity(p, time.Time{})
			if err != nil {
				fmt.Fprintf(&b, "ParseSessionActivity error: %v\n", err)
			}
			for _, s := range sessions {
				row, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "session %s\n", row)
			}
		}
	}
	compattest.Golden(t, "adapter_output.golden", b.String())
}
