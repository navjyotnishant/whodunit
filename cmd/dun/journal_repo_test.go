package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// appendEntry writes one journal entry for a repo, opening and closing its
// own writer so a test can seed two repositories without holding handles.
func appendEntry(t *testing.T, dataDir, repoID string, e journal.Entry) {
	t.Helper()
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(e); err != nil {
		t.Fatal(err)
	}
}

// NAV-51 criteria 4 and 5. Purge deletes from a global store scoped by
// repo id, so a bug here silently destroys another project's history —
// which is why this is tested against two real repositories rather than
// asserted from the code's shape.
func TestPurgeOtherRepoLeavesThisRepoIntact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)

	mine := newRepo(t, "mine")
	theirs := newRepo(t, "theirs")

	mineID, _, err := resolveRepo(mine)
	if err != nil {
		t.Fatal(err)
	}
	theirsID, _, err := resolveRepo(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if mineID == theirsID {
		t.Fatal("two distinct repos produced the same id")
	}

	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, id := range []string{mineID, theirsID} {
		appendEntry(t, dataDir, id, journal.Entry{
			Timestamp: now,
			Agent:     "claude-code",
			Event:     "tool_use",
			Tool:      "Edit",
			File:      "a.go",
		})
	}

	// Purge the OTHER repository, from outside it.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(mine); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newJournalCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"purge", "--repo", theirs})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// Criterion 5: it must have said which repository it was purging,
	// before the past-tense confirmation.
	if !strings.Contains(out.String(), theirs) {
		t.Fatalf("purge did not name its target:\n%s", out.String())
	}

	// Criterion 4: only the target lost rows.
	left, err := journal.ReadRange(dataDir, mineID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("purging another repo removed this repo's entries: %d left, want 1", len(left))
	}

	gone, err := journal.ReadRange(dataDir, theirsID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 0 {
		t.Fatalf("target repo still has %d entries", len(gone))
	}
}

// Criterion 1: show reads another repository's entries from anywhere.
func TestShowOtherRepoFromElsewhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)

	here := newRepo(t, "here")
	there := newRepo(t, "there")

	thereID, _, err := resolveRepo(there)
	if err != nil {
		t.Fatal(err)
	}
	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	appendEntry(t, dataDir, thereID, journal.Entry{
		Timestamp: time.Now().UTC(),
		Agent:     "claude-code",
		Event:     "tool_use",
		Tool:      "Write",
		File:      "elsewhere.go",
	})

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(here); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newJournalCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"show", "--repo", there})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "elsewhere.go") {
		t.Fatalf("did not read the other repository's journal:\n%s", out.String())
	}
}

// A bad --repo fails loudly rather than printing an empty journal, which
// would be indistinguishable from a repository with nothing recorded.
func TestShowBadRepoFlagErrors(t *testing.T) {
	var out bytes.Buffer
	cmd := newJournalCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"show", "--repo", "/definitely/not/here"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a nonexistent --repo path")
	}
}
