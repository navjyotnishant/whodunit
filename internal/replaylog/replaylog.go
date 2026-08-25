// Author: Navjyot Nishant
// Created: 2026-08-25
// Last updated: 2026-08-25
// Description: The durable record of attributions that failed, so a later
// run can reapply what was missed.

// Package replaylog records determinations that failed, in a file nothing
// rotates away.
//
// # Why this is not hooklog
//
// hooklog already records what the hooks did, including every error they
// swallowed, and for diagnosis that is the right shape. It is the wrong
// home for this for two specific reasons:
//
//   - It rotates and purges. rotate(), Purge() and PurgeRepo() all exist
//     by design, because a diagnostic log needs a size ceiling. A replay
//     record that can be aged out is not a replay record.
//   - It is a stream of what happened, not a list of what is outstanding.
//     Finding the failures in it means reading every line and classifying
//     prose, which is how WHO-210's backfill worked and why that backfill
//     could only ever be an estimate.
//
// So this is a separate file with a narrower promise: every entry is a
// determination that failed, written once, never rewritten, and never
// removed by this package.
//
// # Append-only, and what that does and does not mean
//
// Entries are appended and nothing here edits or truncates the file. That
// is a property of this code, not of the filesystem: anyone with write
// access can still edit the file by hand, and no checksum here would stop
// them, only notice. The guarantee is that whodunit does not rewrite its
// own record of its own failures — which is the failure mode that
// matters, because a tool that can quietly drop its mistakes cannot be
// trusted about the rest.
//
// # Never the thing that blocks
//
// Every function swallows its own errors, exactly as hooklog does. A
// commit must never fail because attribution failed, and it must
// certainly never fail because RECORDING that attribution failed itself
// failed. The cost is that a broken replay log is invisible here;
// `dun status` reports it instead, since the inability to record failures
// is the one failure that must not be silent.
//
// # What is never written
//
// No prompt text, no file contents (NAV-25). An entry holds paths,
// counts, and identifiers - enough to run the determination again, and
// nothing that would make this file worth stealing.
package replaylog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/navjyotnishant/whodunit/internal/spec"
)

// Entry is one determination that did not produce an attribution.
//
// Deliberately not a hooklog.Entry: that type is shaped for reading, with
// a prose Detail field. This one is shaped for re-running, so every field
// a replay needs is its own key rather than something to parse back out
// of a sentence.
type Entry struct {
	// CommitSHA is empty when the entry is written from
	// prepare-commit-msg, which runs before the commit exists. The
	// commit-msg hook fills it in afterwards on a second entry; a replay
	// matches the two by RepoID and Time.
	//
	// Recorded rather than derived because the alternative is joining on
	// a timestamp, which is what WHO-210's backfill had to do and why it
	// could classify only 92 of 102 commits.
	CommitSHA string `json:"commit_sha,omitempty"`

	RepoID string    `json:"repo_id"`
	Time   time.Time `json:"time"`

	// Status is why this failed, from the same vocabulary the trailer
	// uses. Only unmatched and degraded are ever written here: the other
	// unattributed statuses are answers, not failures, and a log of
	// things that went wrong should not fill up with things that went
	// right.
	Status spec.Status `json:"status"`

	// StagedFiles is what the determination was asked about. A replay
	// needs these to know which files to match against, and they are the
	// reason an entry can be re-run at all.
	StagedFiles []string `json:"staged_files,omitempty"`

	// AgentLines is how many agent line hashes were available. The
	// number that separates "an agent was working elsewhere" from
	// "no agent was anywhere near this".
	AgentLines int `json:"agent_lines"`

	// Err is why attribution failed outright, set only for degraded.
	// A message, never a stack: this file is a work list, not a crash
	// report.
	Err string `json:"err,omitempty"`

	// Replayed marks an entry a later run resolved. Written as a NEW
	// entry rather than by editing the original - the file is
	// append-only, and a failure that was later fixed is still a failure
	// that happened. A reader folds the two together.
	Replayed bool `json:"replayed,omitempty"`
}

// Dir is where the replay log lives, beside the hook log rather than
// inside it: same directory, different lifecycle.
func Dir(home string) string { return filepath.Join(home, "log") }

func path(home string) string { return filepath.Join(Dir(home), "replay.log") }

// Record appends one failed determination.
//
// Silent on every error, for the reason in the package doc. Callers do
// not check a return value because there is nothing useful they could do
// with it on the commit path.
func Record(home string, e Entry) {
	// Only failures. Recording an answer here would turn the work list
	// into a second copy of the journal, and a replay that re-ran
	// successful determinations would rewrite attributions that were
	// already correct.
	if e.Status != spec.StatusUnmatched && e.Status != spec.StatusDegraded {
		return
	}
	if home == "" {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}

	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		return
	}

	// 0600 for the same reason the journal and hook log are: this names
	// which repositories were worked on and when.
	//
	// O_APPEND is what makes concurrent writes safe here. Two hooks
	// running at once - a commit in one repository while another is
	// mid-push - each append whole lines rather than overwriting at a
	// shared offset.
	f, err := os.OpenFile(path(home), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	b, err := json.Marshal(e)
	if err != nil {
		return
	}

	// Lead with a newline when the file does not end in one. A crash
	// mid-write leaves a partial line, and appending straight onto it
	// would splice the two into a single unparseable record - losing
	// this entry as well as the broken one. Costs a stat and a blank
	// line; saves every subsequent failure from one interrupted write.
	if info, serr := f.Stat(); serr == nil && info.Size() > 0 && !endsWithNewline(path(home)) {
		b = append([]byte{'\n'}, b...)
	}
	_, _ = f.Write(append(b, '\n'))
}

// Outstanding returns the failures no later run has resolved.
//
// Folds the append-only history into a current view: an entry marked
// Replayed cancels the earlier failure it refers to, matched on repo and
// commit. The failures themselves stay in the file - this is a reading,
// not a deletion.
func Outstanding(home string) ([]Entry, error) {
	entries, err := Read(home)
	if err != nil {
		return nil, err
	}

	resolved := map[string]bool{}
	for _, e := range entries {
		if e.Replayed {
			resolved[e.RepoID+"\x00"+e.CommitSHA] = true
		}
	}

	var out []Entry
	for _, e := range entries {
		if e.Replayed {
			continue
		}
		// An entry with no SHA cannot be cancelled by key, so it stays
		// outstanding until one that names it arrives. Reporting it
		// twice is better than dropping a real failure.
		if e.CommitSHA != "" && resolved[e.RepoID+"\x00"+e.CommitSHA] {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Read returns every entry, oldest first.
//
// A malformed line is skipped rather than fatal. This file is written by
// a hook that must never fail a commit, so a line truncated by a crash
// mid-write is an expected state; refusing to read the whole log because
// of one bad line would lose every failure recorded before it.
func Read(home string) ([]Entry, error) {
	b, err := os.ReadFile(path(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Entry
	for _, line := range splitLines(b) {
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// endsWithNewline reports whether the file's last byte is a newline, so a
// partial line left by an interrupted write can be closed off rather than
// appended to.
func endsWithNewline(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return true // unreadable: assume the safe case and do not prepend
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, info.Size()-1); err != nil {
		return true
	}
	return buf[0] == '\n'
}
