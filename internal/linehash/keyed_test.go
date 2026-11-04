package linehash

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/compattest"
)

func testKey(t *testing.T, version int, fill byte) Key {
	t.Helper()
	k, err := NewKey(version, bytes.Repeat([]byte{fill}, MinKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestNewKeyRejectsWeakInput(t *testing.T) {
	if _, err := NewKey(1, make([]byte, MinKeyBytes-1)); err == nil {
		t.Error("short secret accepted")
	}
	if _, err := NewKey(0, make([]byte, MinKeyBytes)); err == nil {
		t.Error("version 0 accepted")
	}
}

func TestNewKeyCopiesTheSecret(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, MinKeyBytes)
	k, err := NewKey(1, secret)
	if err != nil {
		t.Fatal(err)
	}
	before := OfKeyed(k, "main.go", "return nil")
	secret[0] = 8
	if OfKeyed(k, "main.go", "return nil") != before {
		t.Fatal("mutating the caller's slice changed the key")
	}
}

func TestOfKeyedProperties(t *testing.T) {
	k1 := testKey(t, 1, 1)
	k2 := testKey(t, 2, 2)
	line := "return nil"
	if OfKeyed(k1, "a/main.go", line) == OfKeyed(k2, "a/main.go", line) {
		t.Error("different secrets produced the same hash")
	}
	if OfKeyed(k1, "a/main.go", line) == OfKeyed(k1, "b/main.go", line) {
		t.Error("path is not part of the keyed hash")
	}
	if OfKeyed(k1, "a/main.go", line) == Of("a/main.go", line) {
		t.Error("keyed hash equals the unkeyed one")
	}
	if OfKeyed(k1, `a\main.go`, "\t"+line+"  ") != OfKeyed(k1, "a/main.go", line) {
		t.Error("keyed hash does not normalise path and whitespace like Of")
	}
	if k2.Version() != 2 {
		t.Errorf("Version() = %d", k2.Version())
	}
}

func TestOfKeyedPanicsOnZeroKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("zero Key did not panic")
		}
	}()
	OfKeyed(Key{}, "main.go", "return nil")
}

func TestOfTextKeyedSkipsLikeOfText(t *testing.T) {
	k := testKey(t, 1, 1)
	text := "package main\n}\n\n\treturn nil\n"
	if got, want := len(OfTextKeyed(k, "m.go", text)), len(OfText("m.go", text)); got != want {
		t.Fatalf("OfTextKeyed kept %d lines, OfText %d", got, want)
	}
}

func TestRepoRelative(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := RepoRelative(root, filepath.Join(root, "pkg", "x", "y.go"))
	if err != nil || got != "pkg/x/y.go" {
		t.Fatalf("RepoRelative = %q, %v", got, err)
	}
	for _, p := range []string{root, filepath.Dir(root), filepath.Join(filepath.Dir(root), "other.go")} {
		if _, err := RepoRelative(root, p); !errors.Is(err, ErrOutsideRepo) {
			t.Errorf("RepoRelative(%q) err = %v, want ErrOutsideRepo", p, err)
		}
	}
}

// Keyed hashes are stored by Pro, so their output is pinned like Of's.
func TestCompatGoldenKeyedLineHashes(t *testing.T) {
	k := testKey(t, 1, 0x5a)
	var b strings.Builder
	for _, p := range []string{"main.go", "pkg/../main.go", `internal\x.go`, "a b/ünïcode.go"} {
		for _, l := range compatLines {
			fmt.Fprintf(&b, "%q\t%q\t%016x\n", p, l, OfKeyed(k, p, l))
		}
	}
	compattest.Golden(t, "linehash_keyed.golden", b.String())
}

// ADR 0008: open-source dun never computes keyed hashes by default. Any call
// outside this package must sit behind an opt-in and be listed here, so the
// change is reviewed as a change in default behaviour.
var keyedCallersAllowed = map[string]bool{}

func TestCompatDunNeverCallsOfKeyed(t *testing.T) {
	for _, dir := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			if filepath.Base(filepath.Dir(p)) == "linehash" && strings.Contains(filepath.ToSlash(p), "internal/linehash/") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, name := range []string{"OfKeyed(", "OfTextKeyed("} {
				if bytes.Contains(src, []byte(name)) && !keyedCallersAllowed[filepath.ToSlash(p)] {
					t.Errorf("%s calls %s: dun must not compute keyed hashes by default (ADR 0008)", p, strings.TrimSuffix(name, "("))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
