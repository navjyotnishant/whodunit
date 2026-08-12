// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Registers Codex CLI with the adapter registry.

package codex

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter exposes this package through the adapter.Adapter contract.
type Adapter struct{}

func (Adapter) Name() string { return AgentName }

func (Adapter) Root() string { return SessionsDir() }

// SessionDir is the transcript root, not a per-repository directory: Codex
// files sessions by date and records the repository inside each one, so
// there is no directory that belongs to a single repo.
func (Adapter) SessionDir(string) string { return SessionsDir() }

func (Adapter) SessionFiles(cwd string) ([]string, error) { return SessionFiles(cwd) }

func (Adapter) ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	return ParseSince(path, since)
}

func (Adapter) ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	return ParseSessionActivity(path, since)
}

func init() { adapter.Register(Adapter{}) }
