package replaylog

import (
	"testing"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// The hook records whatever Determine concluded; only failures survive
// the filter. Pins the two that must reach the log against the four that
// must not, since that boundary is the whole contract of this package.
func TestTheHookFilterKeepsOnlyFailures(t *testing.T) {
	home := t.TempDir()
	for _, st := range []spec.Status{
		spec.StatusAssisted, spec.StatusUnassisted,
		spec.StatusUninstrumented, spec.StatusUndetermined,
		spec.StatusUnmatched, spec.StatusDegraded,
	} {
		Record(home, Entry{RepoID: "r", CommitSHA: "c" + string(st[0]), Status: st})
	}
	got, err := Outstanding(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want unmatched+degraded only, got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Status != spec.StatusUnmatched && e.Status != spec.StatusDegraded {
			t.Errorf("%s reached the work list", e.Status)
		}
	}
}
