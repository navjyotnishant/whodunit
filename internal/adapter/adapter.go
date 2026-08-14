// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The contract every agent adapter implements, and the registry
// the commands iterate instead of naming one agent.

// Package adapter defines what whodunit needs from an AI coding agent, and
// keeps the list of agents that provide it.
//
// Agents have almost nothing in common on disk. Claude Code writes
// append-only JSONL under a path-slug directory; Codex writes append-only
// JSONL keyed by a cwd recorded inside the file; Antigravity's CLI writes a
// SQLite database per conversation with protobuf payloads. Their edit
// records differ, their session keys differ, and one of them rewrites its
// whole message array on every turn rather than appending.
//
// So the shared abstraction is deliberately thin: find this repository's
// sessions, and turn one session into journal entries. Everything specific
// to a format stays inside that agent's package, where it can be wrong
// without breaking the others.
//
// See docs/adapters/agent-support.md for the per-agent survey this shape
// was derived from.
package adapter

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter reads one agent's local session records.
//
// Implementations are read-only: they never write to an agent's files, and
// never read anything they do not need for attribution. Prompt text and
// file contents are out of scope by construction, not by filtering.
type Adapter interface {
	// Name is the agent identifier that appears in the trailer's `agent=`
	// field, the journal, and the config file. One vocabulary everywhere:
	// "claude-code", "codex", "agy".
	Name() string

	// SessionDir returns the directory this agent stores transcripts for
	// the repository at cwd. Returns "" when the agent is not installed,
	// which is a fact rather than an error — most machines have some
	// agents and not others.
	//
	// Used by the daemon to decide what to watch.
	SessionDir(cwd string) string

	// Root is the agent's transcript root, before any per-repository
	// subdirectory. This is what a path override sets, and what a report
	// must show someone whose location is wrong — the repository
	// subdirectory derived from it is not a value they can correct.
	Root() string

	// SessionFiles returns every session record for the repository at cwd.
	//
	// A missing directory yields no files and no error: the agent simply
	// has not been used here. Only a genuine failure to look — an
	// unreadable directory, a malformed override — is an error.
	SessionFiles(cwd string) ([]string, error)

	// ParseSince turns one session record into journal entries at or after
	// `since`, along with what happened to each tool call.
	//
	// Unrecognized records are skipped rather than fatal. An agent that
	// changes its format must degrade to fewer entries, never to a failed
	// ingest and never to a confident wrong answer — absence of evidence
	// is `undetermined`, not "no AI" (NAV-21).
	ParseSince(path string, since time.Time) ([]journal.Entry, error)

	// ParseSessionActivity summarises one session's engagement: message and
	// tool-call counts, never content (NAV-55).
	//
	// An agent that records no such counts returns nothing, rather than
	// zeros — a real zero and a missing measurement are different claims.
	ParseSessionActivity(path string, since time.Time) ([]journal.Session, error)
}

// registered holds every adapter, in the order they were registered.
//
// A slice rather than a map: iteration order decides the order agents are
// reported in, and a map would shuffle `dun init`'s output between runs for
// no reason.
var registered []Adapter

// Register adds an adapter to the set every command consults. Call it from
// an init function in the agent's package.
//
// Registration rather than a hardcoded list is what lets a command iterate
// agents without importing each one, and what keeps adding an agent from
// touching any file outside its own package.
func Register(a Adapter) {
	registered = append(registered, a)
}

// All returns every registered adapter.
//
// Commands iterate this instead of naming an agent, so an agent added later
// is picked up by ingest, the hooks, the daemon and status without any of
// them changing.
func All() []Adapter {
	out := make([]Adapter, len(registered))
	copy(out, registered)
	return out
}

// ByName returns the adapter with the given agent name, or nil.
// Used where a specific agent is addressed — a config override, or a
// `--agent` flag.
func ByName(name string) Adapter {
	for _, a := range registered {
		if a.Name() == name {
			return a
		}
	}
	return nil
}
