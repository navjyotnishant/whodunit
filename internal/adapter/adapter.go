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
	"fmt"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
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

// optIn holds adapters that are off unless turned on: agents whose stores
// are not only about code, added after the first three (Claude Desktop's
// Cowork, Desktop Commander).
//
// Off by default so that an existing dun install behaves exactly as before
// on a machine that happens to have Claude Desktop: no new store is read and
// no commit's result changes until someone runs
// `dun config set agent.<name>.enabled on`. whodunit Pro turns them all on
// with EnableOptIn.
//
// Consulted after every adapter registered with Register. Packages
// initialise in import-path order, so claude-desktop would otherwise land
// between claude-code and codex, and order decides which session a trailer
// names first when two agents touched the same lines.
var optIn []Adapter

// allOptIn is set by EnableOptIn.
var allOptIn bool

// RegisterOptIn is Register for an adapter that is off until enabled.
func RegisterOptIn(a Adapter) {
	optIn = append(optIn, a)
}

// EnableOptIn turns on every opt-in adapter for this process, whatever the
// config file says. For programs built on the public packages that want
// every agent, which is whodunit Pro's collector. dun never calls it.
func EnableOptIn() { allOptIn = true }

// IsOptIn reports whether the named agent is off unless enabled.
func IsOptIn(name string) bool {
	for _, a := range optIn {
		if a.Name() == name {
			return true
		}
	}
	return false
}

// Enabled reports whether an adapter takes part. Always true for one
// registered with Register; for an opt-in one, true only after EnableOptIn
// or with `agent.<name>.enabled` on in the config file. A config file that
// cannot be read leaves opt-in agents off: turning on a new store because a
// file was unreadable would be the surprise this exists to prevent.
func Enabled(name string) bool {
	if !IsOptIn(name) {
		return true
	}
	if allOptIn {
		return true
	}
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	a, ok := cfg.Agents[name]
	return ok && a.Enabled != nil && *a.Enabled
}

// Known returns every adapter, enabled or not: what `dun config` can name
// and `dun config agents` can report on.
func Known() []Adapter {
	out := make([]Adapter, 0, len(registered)+len(optIn))
	out = append(out, registered...)
	return append(out, optIn...)
}

// All returns every registered adapter.
//
// Commands iterate this instead of naming an agent, so an agent added later
// is picked up by ingest, the hooks, the daemon and status without any of
// them changing.
func All() []Adapter {
	out := make([]Adapter, 0, len(registered)+len(optIn))
	out = append(out, registered...)
	for _, a := range optIn {
		if Enabled(a.Name()) {
			out = append(out, a)
		}
	}
	return out
}

// ByName returns the adapter with the given agent name, or nil.
// Used where a specific agent is addressed — a config override, or a
// `--agent` flag.
//
// Opt-in adapters are found whether or not they are enabled, so a setting can
// name one before it is turned on.
func ByName(name string) Adapter {
	for _, a := range Known() {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

// LineHasher computes a second, opt-in set of hashes for the lines an agent
// produced in one file: canonical absolute path and the produced text, as the
// adapter passes them to linehash.OfText. It exists so pkg/capture can add
// keyed hashes (ADR 0008 in whodunit-pro) while the text is still in memory.
// dun never supplies one.
type LineHasher func(file, text string) []uint64

// keyedParsers maps an agent name to its ParseSince variant that also takes
// a LineHasher, registered next to the adapter itself.
var keyedParsers = map[string]func(path string, since time.Time, keyed LineHasher) ([]journal.Entry, error){}

// RegisterKeyed records an adapter's keyed parse function. Call it from the
// same init function as Register.
func RegisterKeyed(name string, parse func(path string, since time.Time, keyed LineHasher) ([]journal.Entry, error)) {
	keyedParsers[name] = parse
}

// ParseSinceKeyed is a.ParseSince with a LineHasher whose result lands in
// Entry.KeyedLineHashes. A nil hasher gives exactly a.ParseSince.
func ParseSinceKeyed(a Adapter, path string, since time.Time, keyed LineHasher) ([]journal.Entry, error) {
	parse, ok := keyedParsers[a.Name()]
	if !ok {
		return nil, fmt.Errorf("adapter %q does not support keyed parsing", a.Name())
	}
	return parse(path, since, keyed)
}

// HashKeyed applies h when there is one. Adapters call it next to
// linehash.OfText so both hash sets come from the same text under the same
// conditions.
func HashKeyed(h LineHasher, file, text string) []uint64 {
	if h == nil {
		return nil
	}
	return h(file, text)
}
