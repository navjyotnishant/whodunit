// Package attribution determines the highest-confidence AI-Attribution
// trailer for a commit, by looking up staged changes against the local
// journal.
package attribution

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

// lookbackWindow bounds how far back a journal entry can be and still count
// as covering the change about to be committed. Generous, since edits often
// sit staged for a while before commit.
const lookbackWindow = 7 * 24 * time.Hour

// Determine picks a trailer for a commit whose staged files are stagedFiles,
// by checking whether any recent journal entry touched one of those files.
// When stagedHunkHashes is non-empty and at least one journal entry's hash
// appears in it, confidence upgrades from observed (same file touched) to
// intersected (the exact text the agent produced is what got staged) —
// NAV-26/NAV-27's highest-confidence method. Pass a nil/empty map to skip
// hunk matching and always get observed at best (e.g. when git diff isn't
// available). Returns method=undetermined if the journal has no relevant
// coverage — per NAV-21, absence must never be silently upgraded.
func Determine(entries []journal.Entry, stagedFiles []string, stagedHunkHashes map[string]bool, now time.Time) spec.Trailer {
	staged := map[string]bool{}
	for _, f := range stagedFiles {
		staged[f] = true
	}

	since := now.Add(-lookbackWindow)
	var relevant []journal.Entry
	intersected := false
	for _, e := range entries {
		if e.Event != "tool_use" || e.Timestamp.Before(since) {
			continue
		}
		if !staged[e.File] {
			continue
		}
		relevant = append(relevant, e)
		if e.HunkHash != "" && stagedHunkHashes[e.HunkHash] {
			intersected = true
		}
	}

	if len(relevant) == 0 {
		return spec.Undetermined()
	}

	agent, version, session := relevant[0].Agent, relevant[0].AgentVersion, relevant[0].Session
	var linesAdded, linesRemoved int
	for _, e := range relevant {
		linesAdded += e.LinesAdded
		linesRemoved += e.LinesRemoved
	}

	ratio := 1.0
	if linesAdded+linesRemoved == 0 {
		ratio = 0
	}

	method := spec.MethodObserved
	if intersected {
		method = spec.MethodIntersected
	}

	return spec.Trailer{
		Status:  spec.StatusAssisted,
		Method:  method,
		Agent:   agent,
		Version: version,
		Session: session,
		Ratio:   ratio,
		Extra:   map[string]string{},
	}
}
