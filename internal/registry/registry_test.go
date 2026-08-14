package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// The registry is user-created, opt-in, and not regenerable from anywhere:
// it is the list of repositories someone chose to instrument. Losing it
// means every cross-repo command goes blind until they re-run `dun init`
// in each one, and nothing tells them that is what happened.
//
// write used a bare os.WriteFile, which truncates before it writes. Two
// `dun init` runs in different repositories at the same moment, or a crash
// mid-write, leaves a truncated repos.json that fails to parse — and then
// List errors and every cross-repo command fails.
func TestConcurrentAddsDoNotCorruptTheRegistry(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Add(fmt.Sprintf("repo-%d", i),
				fmt.Sprintf("/path/to/repo-%d", i), time.Now()); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("a concurrent Add failed: %v", err)
	}

	// Whatever interleaving happened, the file must still parse. A registry
	// that fails to unmarshal takes every cross-repo command down with it.
	entries, err := List()
	if err != nil {
		t.Fatalf("the registry no longer parses after concurrent writes: %v\n\n"+
			"This file is not regenerable — it is the list of repositories "+
			"the user chose to instrument.", err)
	}
	if len(entries) == 0 {
		t.Fatal("every entry was lost")
	}
}

// A reader must never observe a half-written file, which is what truncating
// in place allows.
func TestReadingWhileWritingNeverSeesAPartialRegistry(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	// Enough entries that a write is not instantaneous.
	for i := 0; i < 50; i++ {
		if err := Add(fmt.Sprintf("seed-%d", i), fmt.Sprintf("/seed/%d", i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = Add(fmt.Sprintf("churn-%d", i), fmt.Sprintf("/churn/%d", i), time.Now())
		}
	}()

	var failures int
	for i := 0; i < 100; i++ {
		if _, err := List(); err != nil {
			failures++
			t.Logf("read %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	if failures > 0 {
		t.Fatalf("%d of 100 reads saw an unparseable registry while another "+
			"writer was active", failures)
	}
}
