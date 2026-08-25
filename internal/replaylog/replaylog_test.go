package replaylog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Only failures belong here. An unassisted commit is an answer, and a work
// list that fills with answers is not a work list.
func TestOnlyFailuresAreRecorded(t *testing.T) {
	home := t.TempDir()
	for _, st := range []spec.Status{
		spec.StatusAssisted, spec.StatusUnassisted,
		spec.StatusUninstrumented, spec.StatusUndetermined,
	} {
		Record(home, Entry{RepoID: "r", Status: st})
	}
	got, err := Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("recorded %d non-failures: %+v", len(got), got)
	}

	Record(home, Entry{RepoID: "r", Status: spec.StatusUnmatched})
	Record(home, Entry{RepoID: "r", Status: spec.StatusDegraded, Err: "journal unreadable"})
	got, _ = Read(home)
	if len(got) != 2 {
		t.Errorf("want 2 failures, got %d", len(got))
	}
}

// The property the whole package exists for. A replay marks an entry
// resolved by APPENDING, never by editing, so the record of what went
// wrong survives being fixed.
func TestReplayAppendsAndNeverRewrites(t *testing.T) {
	home := t.TempDir()
	Record(home, Entry{RepoID: "r", CommitSHA: "abc", Status: spec.StatusUnmatched})

	before, err := os.ReadFile(filepath.Join(Dir(home), "replay.log"))
	if err != nil {
		t.Fatal(err)
	}

	Record(home, Entry{RepoID: "r", CommitSHA: "abc", Status: spec.StatusUnmatched, Replayed: true})

	after, _ := os.ReadFile(filepath.Join(Dir(home), "replay.log"))
	if !strings.HasPrefix(string(after), string(before)) {
		t.Error("the original entry was rewritten; the log must only grow")
	}

	out, err := Outstanding(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("a replayed failure is still outstanding: %+v", out)
	}
	all, _ := Read(home)
	if len(all) != 2 {
		t.Errorf("want both entries kept, got %d", len(all))
	}
}

// An entry written before the commit exists carries no SHA, so nothing can
// cancel it by key. It stays outstanding rather than being silently
// dropped - reporting a fixed failure twice is better than losing a real
// one.
func TestEntryWithoutSHAStaysOutstanding(t *testing.T) {
	home := t.TempDir()
	Record(home, Entry{RepoID: "r", Status: spec.StatusUnmatched, AgentLines: 12})
	Record(home, Entry{RepoID: "r", CommitSHA: "other", Status: spec.StatusUnmatched, Replayed: true})

	out, _ := Outstanding(home)
	if len(out) != 1 {
		t.Fatalf("want the SHA-less failure kept, got %+v", out)
	}
	if out[0].AgentLines != 12 {
		t.Errorf("replay needs the agent-line count, got %+v", out[0])
	}
}

// A line truncated by a crash mid-write must not cost every failure
// recorded before it.
func TestATruncatedLineDoesNotLoseTheRest(t *testing.T) {
	home := t.TempDir()
	Record(home, Entry{RepoID: "r", CommitSHA: "a", Status: spec.StatusUnmatched})
	p := filepath.Join(Dir(home), "replay.log")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"repo_id":"r","status":"unmat`)
	f.Close()
	Record(home, Entry{RepoID: "r", CommitSHA: "b", Status: spec.StatusUnmatched})

	got, err := Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want both intact entries, got %d: %+v", len(got), got)
	}
}

// Never blocks: an unwritable home is a degraded log, not a failed commit.
func TestAnUnwritableHomeIsSilent(t *testing.T) {
	Record("", Entry{RepoID: "r", Status: spec.StatusUnmatched})
	Record(filepath.Join(t.TempDir(), "nope\x00bad"), Entry{RepoID: "r", Status: spec.StatusDegraded})
}

// No prompt text, no file contents (NAV-25). Paths and counts only, which
// is what a replay needs and all it needs.
func TestEntryCarriesNoContent(t *testing.T) {
	home := t.TempDir()
	Record(home, Entry{
		RepoID: "r", Status: spec.StatusUnmatched,
		StagedFiles: []string{"internal/secret.go"}, AgentLines: 3,
		Time: time.Now(),
	})
	b, _ := os.ReadFile(filepath.Join(Dir(home), "replay.log"))
	for _, banned := range []string{"prompt", "content", "message", "body", "diff"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("entry carries a %q field", banned)
		}
	}
}
