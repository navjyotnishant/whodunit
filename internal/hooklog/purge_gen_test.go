package hooklog

import (
	"os"
	"testing"
)

var osRename = os.Rename

// Entries for the same repo in BOTH generations. The rewrite of the second
// file must not be skipped because the first already removed something.
func TestPurgeAcrossBothGenerations(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{RepoID: "bbb", Hook: "pre-push", Detail: "old gen"})
	if err := rotateForce(home); err != nil {
		t.Fatal(err)
	}
	Write(home, Entry{RepoID: "bbb", Hook: "pre-push", Detail: "current gen"})
	Write(home, Entry{RepoID: "aaa", Hook: "pre-push", Detail: "keep"})

	removed, err := PurgeRepo(home, "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed %d, want 2 (one per generation)", removed)
	}
	entries, _ := Read(home, 0)
	for _, e := range entries {
		if e.RepoID == "bbb" {
			t.Fatalf("a purged entry survived in one generation: %+v", e)
		}
	}
}

// rotateForce moves the current log aside regardless of size.
func rotateForce(home string) error {
	return osRename(path(home), oldPath(home))
}

// The case the running-total bug hid: matches in the OLDER generation only.
// PurgeRepo reads the current file first, finds nothing, and must still
// rewrite the older one rather than skipping on a zero count.
func TestPurgeWhenOnlyTheOlderGenerationMatches(t *testing.T) {
	home := t.TempDir()
	Write(home, Entry{RepoID: "bbb", Hook: "pre-push", Detail: "old gen only"})
	if err := rotateForce(home); err != nil {
		t.Fatal(err)
	}
	Write(home, Entry{RepoID: "aaa", Hook: "pre-push", Detail: "current, unrelated"})

	removed, err := PurgeRepo(home, "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}

	entries, _ := Read(home, 0)
	for _, e := range entries {
		if e.RepoID == "bbb" {
			t.Fatal("the purged entry survived in the older generation")
		}
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the 1 unrelated one kept", len(entries))
	}
}
