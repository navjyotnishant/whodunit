package adapter

import (
	"errors"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

type fake struct {
	name  string
	dir   string
	files []string
	err   error
}

func (f fake) Name() string             { return f.name }
func (f fake) SessionDir(string) string { return f.dir }
func (f fake) Root() string             { return f.dir }
func (f fake) SessionFiles(string) ([]string, error) {
	return f.files, f.err
}
func (f fake) ParseSince(string, time.Time) ([]journal.Entry, error) { return nil, nil }
func (f fake) ParseSessionActivity(string, time.Time) ([]journal.Session, error) {
	return nil, nil
}

// Registration order is the reporting order. A map would shuffle `dun init`
// output between runs for no reason, so this is asserted rather than left
// to chance.
func TestAllPreservesRegistrationOrder(t *testing.T) {
	defer restore(t)()
	registered = nil

	Register(fake{name: "first"})
	Register(fake{name: "second"})
	Register(fake{name: "third"})

	got := All()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("got %d adapters, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name() != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name(), w)
		}
	}
}

// All returns a copy: a caller that appends to the result must not be able
// to register an adapter as a side effect.
func TestAllReturnsACopy(t *testing.T) {
	defer restore(t)()
	registered = nil
	Register(fake{name: "only"})

	got := All()
	got = append(got, fake{name: "sneaked-in"})
	_ = got

	if n := len(All()); n != 1 {
		t.Fatalf("registry grew to %d after a caller appended to All()'s result", n)
	}
}

func TestByName(t *testing.T) {
	defer restore(t)()
	registered = nil
	Register(fake{name: "claude-code"})
	Register(fake{name: "codex"})

	if a := ByName("codex"); a == nil || a.Name() != "codex" {
		t.Fatalf("ByName(codex) = %v", a)
	}
	if a := ByName("nope"); a != nil {
		t.Fatalf("ByName on an unknown agent returned %v, want nil", a)
	}
}

// An empty registry must behave, not panic: it is the state during a test
// that clears it, and the state of a build that registers nothing.
func TestEmptyRegistry(t *testing.T) {
	defer restore(t)()
	registered = nil

	if n := len(All()); n != 0 {
		t.Fatalf("All() on empty registry returned %d", n)
	}
	if a := ByName("anything"); a != nil {
		t.Fatalf("ByName on empty registry returned %v", a)
	}
}

// An adapter that cannot look must be distinguishable from one that looked
// and found nothing — the caller decides, but the interface has to carry
// the difference (NAV-21).
func TestSessionFilesDistinguishesErrorFromEmpty(t *testing.T) {
	notInstalled := fake{name: "absent"}
	broken := fake{name: "broken", err: errors.New("permission denied")}

	files, err := notInstalled.SessionFiles("/repo")
	if err != nil || len(files) != 0 {
		t.Fatalf("not-installed agent: got (%v, %v), want (empty, nil)", files, err)
	}

	if _, err := broken.SessionFiles("/repo"); err == nil {
		t.Fatal("an unreadable agent directory returned no error")
	}
}

// restore puts the package registry back after a test mutates it, so tests
// stay independent of each other and of any real adapter registered by an
// imported package.
func restore(t *testing.T) func() {
	t.Helper()
	saved := registered
	return func() { registered = saved }
}
