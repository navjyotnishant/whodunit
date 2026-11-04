package capture

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/compattest"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixtures are the adapter test transcripts, copied with a fixed mtime
// because agy dates entries by its database file's mtime.
func fixtures(t *testing.T) map[string][]string {
	t.Helper()
	base := filepath.Join("..", "..", "internal", "adapter")
	glob := func(p string) []string {
		m, _ := filepath.Glob(filepath.Join(base, p))
		sort.Strings(m)
		return m
	}
	src := map[string][]string{
		"agy":         {filepath.Join(base, "agy", "testdata", "conversation.db")},
		"claude-code": glob("claudecode/testdata/*.jsonl"),
		"codex":       {filepath.Join(base, "codex", "testdata", "rollout-basic.jsonl")},
	}
	out := map[string][]string{}
	for agent, paths := range src {
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), filepath.Base(p))
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(dst, fixedTime, fixedTime); err != nil {
				t.Fatal(err)
			}
			out[agent] = append(out[agent], dst)
		}
	}
	return out
}

func testKey(t *testing.T) linehash.Key {
	t.Helper()
	k, err := linehash.NewKey(1, bytes.Repeat([]byte{0x5a}, linehash.MinKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestAgentsInDunsOrder(t *testing.T) {
	var names []string
	for _, a := range Agents() {
		names = append(names, a.Name())
	}
	if got := strings.Join(names, ","); got != "agy,claude-code,codex" {
		t.Fatalf("Agents() = %s", got)
	}
	if ByName("codex") == nil || ByName("nope") != nil {
		t.Fatal("ByName")
	}
}

// Keyed parsing adds keyed hashes and changes nothing else, and the default
// path never fills them.
func TestParseKeyedOnlyAddsKeyedHashes(t *testing.T) {
	fx := fixtures(t)
	key := testKey(t)
	withKeyed := 0
	for _, a := range Agents() {
		for _, p := range fx[a.Name()] {
			plain, err := a.ParseSince(p, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			keyed, err := ParseKeyed(a, p, time.Time{}, Keyed{Key: key, RepoRoot: string(filepath.Separator)})
			if err != nil {
				t.Fatal(err)
			}
			if len(plain) != len(keyed) {
				t.Fatalf("%s %s: %d entries plain, %d keyed", a.Name(), p, len(plain), len(keyed))
			}
			for i := range plain {
				if plain[i].KeyedLineHashes != nil {
					t.Fatalf("%s: default ParseSince filled KeyedLineHashes", a.Name())
				}
				k := keyed[i]
				if len(k.KeyedLineHashes) > 0 {
					withKeyed++
				}
				if len(k.KeyedLineHashes) != len(k.LineHashes) && len(k.KeyedLineHashes) != 0 {
					t.Errorf("%s entry %d: %d keyed vs %d unkeyed hashes", a.Name(), i, len(k.KeyedLineHashes), len(k.LineHashes))
				}
				k.KeyedLineHashes = nil
				if !reflect.DeepEqual(plain[i], k) {
					t.Errorf("%s entry %d differs beyond KeyedLineHashes:\nplain %+v\nkeyed %+v", a.Name(), i, plain[i], k)
				}
			}
		}
	}
	if withKeyed == 0 {
		t.Fatal("no fixture produced keyed hashes; the test proves nothing")
	}
}

func TestParseKeyedSkipsFilesOutsideTheRepository(t *testing.T) {
	fx := fixtures(t)
	root := filepath.Join(t.TempDir(), "some-other-repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range Agents() {
		for _, p := range fx[a.Name()] {
			entries, err := ParseKeyed(a, p, time.Time{}, Keyed{Key: testKey(t), RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.KeyedLineHashes != nil {
					t.Fatalf("%s: keyed hashes for %s, outside %s", a.Name(), e.File, root)
				}
			}
		}
	}
}

func TestParseKeyedRequiresKeyAndRoot(t *testing.T) {
	a := ByName("codex")
	if _, err := ParseKeyed(a, "x", time.Time{}, Keyed{RepoRoot: "/"}); err == nil {
		t.Error("zero key accepted")
	}
	if _, err := ParseKeyed(a, "x", time.Time{}, Keyed{Key: testKey(t)}); err == nil {
		t.Error("empty root accepted")
	}
}

// Keyed hashes are what dun-pro uploads, so their values are pinned.
func TestCompatGoldenKeyedCapture(t *testing.T) {
	fx := fixtures(t)
	var b strings.Builder
	for _, a := range Agents() {
		for _, p := range fx[a.Name()] {
			entries, err := ParseKeyed(a, p, time.Time{}, Keyed{Key: testKey(t), RepoRoot: string(filepath.Separator)})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "== %s %s\n", a.Name(), filepath.Base(p))
			for _, e := range entries {
				hs := make([]string, len(e.KeyedLineHashes))
				for i, h := range e.KeyedLineHashes {
					hs[i] = fmt.Sprintf("%016x", h)
				}
				fmt.Fprintf(&b, "%s %s: %s\n", e.Tool, filepath.ToSlash(e.File), strings.Join(hs, " "))
			}
		}
	}
	compattest.Golden(t, "capture_keyed.golden", b.String())
}
