package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/testmode"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	return home
}

func TestListOnMissingRegistry(t *testing.T) {
	isolate(t)

	entries, err := List()
	if err != nil {
		t.Fatalf("List on missing registry = %v, want nil error", err)
	}
	if len(entries) != 0 {
		t.Errorf("List on missing registry = %v, want empty", entries)
	}
}

func TestAddThenList(t *testing.T) {
	isolate(t)
	now := time.Now()

	if err := Add("sha-a", "/repos/a", now); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add("sha-b", "/repos/b", now); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Path != "/repos/a" || entries[1].Path != "/repos/b" {
		t.Errorf("entries not sorted by path: %+v", entries)
	}
	if entries[0].InstrumentedAt.IsZero() {
		t.Error("InstrumentedAt not recorded")
	}
}

func TestAddIsIdempotentPerRepo(t *testing.T) {
	isolate(t)
	now := time.Now()

	if err := Add("sha-a", "/repos/a", now); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add("sha-a", "/repos/a", now); err != nil {
		t.Fatalf("Add (again): %v", err)
	}

	entries, _ := List()
	if len(entries) != 1 {
		t.Fatalf("re-adding the same repo produced %d entries, want 1", len(entries))
	}
}

func TestAddUpdatesPathForAMovedRepo(t *testing.T) {
	// A repo that moved is still the same repo — its identity is the root
	// commit, not its location.
	isolate(t)
	now := time.Now()

	if err := Add("sha-a", "/old/location", now); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add("sha-a", "/new/location", now); err != nil {
		t.Fatalf("Add (moved): %v", err)
	}

	entries, _ := List()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry after move, got %d", len(entries))
	}
	if entries[0].Path != "/new/location" {
		t.Errorf("Path = %q, want the updated location", entries[0].Path)
	}
}

func TestAddRequiresRepoID(t *testing.T) {
	isolate(t)
	if err := Add("", "/repos/a", time.Now()); err == nil {
		t.Error("Add with an empty repo id = nil error, want a refusal")
	}
}

func TestRemove(t *testing.T) {
	isolate(t)
	now := time.Now()
	Add("sha-a", "/repos/a", now)
	Add("sha-b", "/repos/b", now)

	removed, err := Remove("sha-a")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Error("Remove reported nothing removed")
	}

	entries, _ := List()
	if len(entries) != 1 || entries[0].RepoID != "sha-b" {
		t.Errorf("after removing sha-a, entries = %+v", entries)
	}
}

func TestRemoveUnknownRepoIsNotAnError(t *testing.T) {
	isolate(t)
	removed, err := Remove("never-added")
	if err != nil {
		t.Fatalf("Remove unknown = %v, want nil error", err)
	}
	if removed {
		t.Error("Remove reported a removal that did not happen")
	}
}

func TestRegistryFileIsNotWorldReadable(t *testing.T) {
	testmode.SkipIfNoPermissionBits(t)
	// The registry lists local paths of everything being tracked — same
	// sensitivity as the journal.
	home := isolate(t)
	if err := Add("sha-a", "/repos/a", time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	info, err := os.Stat(filepath.Join(home, "repos.json"))
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("repos.json mode = %o, want 600", perm)
	}
}

func TestCorruptRegistryReportsPath(t *testing.T) {
	home := isolate(t)
	p := filepath.Join(home, "repos.json")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := List()
	if err == nil {
		t.Fatal("List on a corrupt registry = nil error, want an error")
	}
	// The error has to name the file, or the user cannot find what to fix.
	if !containsPath(err.Error(), p) {
		t.Errorf("error %q does not name the registry path %q", err, p)
	}
}

func containsPath(msg, path string) bool {
	return len(msg) > 0 && len(path) > 0 && (msg == path || contains(msg, path))
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
