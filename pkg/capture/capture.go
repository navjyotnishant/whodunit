// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 access to agent transcript capture.

// Package capture reads coding agents' local transcripts the way dun does,
// for programs outside this module (spec 002 in whodunit-pro, task T001).
//
// It is a thin wrapper: the adapters are the ones dun ingests with, in the
// same order, so an entry here is the entry dun would journal. The one
// addition is opt-in: ParseKeyed also fills Entry.KeyedLineHashes with
// linehash.OfKeyed hashes over repo-relative paths, computed while the agent's
// text is still in memory. dun itself never asks for them.
//
// Nothing here stores or transmits anything, and an Entry never carries the
// lines themselves.
//
// API stability: v0. Names may still change before whodunit 1.0.
package capture

import (
	"fmt"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"

	// The adapters dun registers, in the order dun registers them.
	_ "github.com/navjyotnishant/whodunit/internal/adapter/agy"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
)

// Entry is one observed agent action, as dun journals it.
type Entry = journal.Entry

// Session is one agent session's activity counts. Never content.
type Session = journal.Session

// Agent reads one coding agent's local transcripts.
type Agent = adapter.Adapter

// Agents returns every supported agent, in dun's order.
func Agents() []Agent { return adapter.All() }

// ByName returns the agent with that name ("claude-code", "codex", "agy"),
// or nil.
func ByName(name string) Agent { return adapter.ByName(name) }

// Keyed says how to compute keyed line hashes.
type Keyed struct {
	// Key is the organisation's line-hash key (linehash.NewKey). Required.
	Key linehash.Key

	// RepoRoot is the repository the entries belong to. Paths are made
	// relative to it, so the same line hashes the same in every clone.
	RepoRoot string
}

// ParseKeyed is Agent.ParseSince plus keyed hashes in Entry.KeyedLineHashes.
//
// Every other field of every entry is exactly what ParseSince returns. An
// edit to a file outside RepoRoot gets no keyed hashes: it belongs to another
// repository, whose key and root this call does not have.
func ParseKeyed(a Agent, path string, since time.Time, k Keyed) ([]Entry, error) {
	if k.Key.IsZero() {
		return nil, fmt.Errorf("capture: ParseKeyed needs a key from linehash.NewKey")
	}
	if k.RepoRoot == "" {
		return nil, fmt.Errorf("capture: ParseKeyed needs a repository root")
	}
	p, ok := a.(adapter.KeyedParser)
	if !ok {
		return nil, fmt.Errorf("capture: agent %q does not support keyed parsing", a.Name())
	}
	return p.ParseSinceKeyed(path, since, func(file, text string) []uint64 {
		rel, err := linehash.RepoRelative(k.RepoRoot, file)
		if err != nil {
			return nil
		}
		return linehash.OfTextKeyed(k.Key, rel, text)
	})
}
