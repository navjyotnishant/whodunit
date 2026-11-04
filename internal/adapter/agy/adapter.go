// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Registers Antigravity CLI (agy) with the adapter registry.

package agy

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter exposes this package through the adapter.Adapter contract.
type Adapter struct{}

func (Adapter) Name() string { return AgentName }

func (Adapter) Root() string { return ConversationsDir() }

// SessionDir is the conversations root, not a per-repository directory:
// agy files conversations by id and records the repository only through
// the absolute paths it edited.
func (Adapter) SessionDir(string) string { return ConversationsDir() }

func (Adapter) SessionFiles(cwd string) ([]string, error) { return SessionFiles(cwd) }

func (Adapter) ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	return ParseSince(path, since)
}

func (Adapter) ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	return ParseSessionActivity(path, since)
}

func init() { adapter.Register(Adapter{}) }
