// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The record of what the hooks did, and of every error they
// swallowed.

// Package hooklog records what whodunit's git hooks did, including the
// errors they deliberately discarded.
//
// # Why this exists
//
// A commit must never fail because attribution failed. That discipline is
// right, and it was implemented as "never mention" rather than "never
// block" — which are different requirements. Every failure inside a hook
// was dropped on the floor:
//
//	_, _, _ = ingestSince(since, func(string, error) {})
//	continue // an agent we cannot look at is not evidence of absence
//
// So an unreadable transcript, a corrupt journal, a permissions problem and
// a genuinely AI-free commit all produced the identical output —
// status=undetermined — with nothing anywhere to tell them apart. Someone
// whose attribution silently stopped had no way to find out, which is
// NAV-21's failure arriving through the back door: absence of evidence made
// indistinguishable from evidence of absence.
//
// # Never the thing that blocks
//
// Every function here swallows its own errors and returns nothing. A log
// that can fail a commit is worse than no log, so a full disk, a read-only
// home or a corrupt file all degrade to not logging.
//
// That is the same discipline the hooks follow, applied to the thing
// recording them. It does mean a broken log is itself invisible; the
// alternative — surfacing it — would put logging on the critical path of
// every commit, which is precisely what this must not be.
//
// # What is never written
//
// No prompt text, no file contents, no commit messages. A log is exactly
// where those leak in, because at the moment of writing they read as
// debugging context rather than as a privacy boundary (NAV-25). File paths
// and counts are already in the journal and are in scope; message content
// is not, and there is no code path here that accepts it.
package hooklog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Level is how much an entry matters.
type Level string

const (
	LevelInfo  Level = "info"  // something ran and worked
	LevelWarn  Level = "warn"  // something failed and was survived
	LevelPanic Level = "panic" // a hook recovered from a crash
)

// Entry is one recorded action.
type Entry struct {
	Time   time.Time `json:"time"`
	Level  Level     `json:"level"`
	Hook   string    `json:"hook"` // prepare-commit-msg, pre-push, …
	RepoID string    `json:"repo_id,omitempty"`
	Repo   string    `json:"repo,omitempty"` // path, for reading
	Event  string    `json:"event"`          // ingest, determine, sync, panic
	Detail string    `json:"detail,omitempty"`

	// Stack is captured only for a recovered panic. A recovered panic with
	// no stack is a crash traded for a silent no-op, which conceals rather
	// than fixes.
	Stack string `json:"stack,omitempty"`
}

// maxBytes bounds the file. An instrumented machine runs these hooks on
// every commit and every push, forever, so an unbounded file is a slow leak
// that nobody notices until a disk fills.
//
// Rotation keeps one previous generation, so the window is up to twice
// this. A megabyte holds several thousand entries — weeks of ordinary use,
// and long enough that the answer to "when did this break" is still in it.
const maxBytes = 1 << 20

// Dir is where the log lives, given the whodunit home.
func Dir(home string) string { return filepath.Join(home, "log") }

func path(home string) string    { return filepath.Join(Dir(home), "hooks.log") }
func oldPath(home string) string { return filepath.Join(Dir(home), "hooks.log.1") }

// Write appends one entry. Every error is swallowed: see the package
// documentation.
func Write(home string, e Entry) {
	if home == "" {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Level == "" {
		e.Level = LevelInfo
	}

	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		return
	}
	rotate(home)

	// 0600 for the same reason the journal is: this records which
	// repositories were worked on and when, which is not something to
	// leave readable by every account on a shared machine.
	f, err := os.OpenFile(path(home), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// rotate moves the log aside once it passes maxBytes, keeping exactly one
// previous generation.
//
// Renaming rather than truncating means a reader holding the file open
// keeps reading a coherent file rather than watching it empty underneath
// them — which matters for `dun log --follow`.
func rotate(home string) {
	info, err := os.Stat(path(home))
	if err != nil || info.Size() < maxBytes {
		return
	}
	_ = os.Rename(path(home), oldPath(home))
}

// Read returns entries most recent first, at most limit of them.
//
// Both generations are read so rotation does not lose the recent past at
// the moment someone goes looking.
func Read(home string, limit int) ([]Entry, error) {
	var all []Entry
	for _, p := range []string{oldPath(home), path(home)} {
		entries, err := readFile(p)
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}

	// Reverse: newest first is the order someone reading a log wants, since
	// the question is nearly always "what just happened".
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func readFile(p string) ([]Entry, error) {
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Entry
	s := bufio.NewScanner(f)
	// Entries are small, but a corrupt file could contain anything; a
	// generous buffer avoids failing the whole read on one long line.
	s.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// A truncated final line is normal — a write interrupted by a
			// crash — and dropping it beats failing the whole read.
			continue
		}
		out = append(out, e)
	}
	return out, s.Err()
}

// PurgeRepo removes one repository's entries, returning how many went.
//
// Called by `dun journal purge`: the promise is that purging removes what
// whodunit recorded, and a log naming every repository and every failure it
// hit would make that promise false if it survived.
//
// Per repository rather than wholesale, because the log is global while
// purge is scoped — deleting the file would erase other projects' records
// as a side effect of purging one.
func PurgeRepo(home, repoID string) (int, error) {
	var removed int

	for _, p := range []string{path(home), oldPath(home)} {
		entries, err := readFile(p)
		if err != nil {
			return removed, err
		}
		if len(entries) == 0 {
			continue
		}

		var keep []Entry
		var hit int
		for _, e := range entries {
			if e.RepoID == repoID {
				hit++
				continue
			}
			keep = append(keep, e)
		}
		removed += hit

		// Per file, so a generation with nothing to remove is left alone.
		// Rewriting it would be correct but pointless work, and it would
		// rewrite a file this call had no reason to touch.
		if hit == 0 {
			continue
		}
		if err := rewrite(p, keep); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// Purge removes the log entirely, for `dun uninstall` and tests.
func Purge(home string) error {
	for _, p := range []string{path(home), oldPath(home)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// rewrite replaces a log file with the given entries.
//
// Written to a temporary file and renamed, so a crash midway leaves the
// original intact rather than a half-written log. A purge that loses
// unrelated repositories' entries to an interrupted write would be worse
// than one that has to be run twice.
func rewrite(p string, entries []Entry) error {
	if len(entries) == 0 {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

// Size reports the bytes currently held across both generations.
func Size(home string) int64 {
	var n int64
	for _, p := range []string{path(home), oldPath(home)} {
		if info, err := os.Stat(p); err == nil {
			n += info.Size()
		}
	}
	return n
}

// String renders one entry as a readable line.
func (e Entry) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %-5s %-19s %s",
		e.Time.Format("2006-01-02 15:04:05"), e.Level, e.Hook, e.Event)
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	return b.String()
}
