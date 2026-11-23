package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// A second sync with nothing new sends no events; --full sends them again;
// a lake that lost the repository's rows gets everything without --full.
func TestSyncSendsOnlyChangesSinceTheLastSync(t *testing.T) {
	chdirToTestRepo(t)

	repoID, err := currentRepoID()
	if err != nil {
		t.Fatal(err)
	}
	dataDir, err := journalDataDir()
	if err != nil {
		t.Fatal(err)
	}
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(journal.Entry{Timestamp: time.Now().Add(-time.Hour), Agent: "claude-code",
		Session: "s1", Event: "tool_use", Tool: "Edit", File: "a.go"}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	lake := filepath.Join(t.TempDir(), "lake.db")
	to := "sqlite:///" + lake

	if got := syncEvents(t, to); got != 1 {
		t.Fatalf("first sync sent %d events, want 1", got)
	}
	// The mark is read back less a minute of slack, so an immediate
	// re-sync still overlaps. Move it past the slack to see what a later
	// sync sends.
	ageMark(t, dataDir, repoID, to)
	if got := syncEvents(t, to); got != 0 {
		t.Fatalf("second sync with nothing new sent %d events, want 0", got)
	}
	if got := syncEvents(t, to, "--full"); got != 1 {
		t.Fatalf("--full sent %d events, want 1", got)
	}

	// The lake is wiped (docker compose down -v): same target, no rows.
	ageMark(t, dataDir, repoID, to)
	if err := os.Remove(lake); err != nil {
		t.Fatal(err)
	}
	if got := syncEvents(t, to); got != 1 {
		t.Fatalf("sync to an empty lake sent %d events, want 1", got)
	}
}

// ageMark moves the mark so the entry written above predates it by more
// than syncMarkSlack.
func ageMark(t *testing.T, dataDir, repoID, to string) {
	t.Helper()
	if err := journal.SetSyncMark(dataDir, repoID, syncTarget(to), time.Now().Add(2*syncMarkSlack)); err != nil {
		t.Fatal(err)
	}
}

var sentEvents = regexp.MustCompile(`sent \d+ commit\(s\), (\d+) event\(s\)`)

func syncEvents(t *testing.T, to string, extra ...string) int {
	t.Helper()
	cmd := newSyncCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--to", to}, extra...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync: %v\n%s", err, out.String())
	}
	m := sentEvents.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no sent line in output:\n%s", out.String())
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func TestSyncTargetCarriesNoCredentials(t *testing.T) {
	got := syncTarget("mysql://merico:secret@db:3306/lake")
	if strings.Contains(got, "secret") || strings.Contains(got, "merico") {
		t.Fatalf("credentials in the sync mark target: %q", got)
	}
	if got != syncTarget("mysql://other:changed@db:3306/lake") {
		t.Fatal("a changed password must keep the same mark")
	}
}
