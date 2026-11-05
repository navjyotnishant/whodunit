// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Registers Claude Desktop (Cowork) with the adapter registry.

package claudedesktop

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter exposes this package through the adapter.Adapter contract.
type Adapter struct{}

func (Adapter) Name() string { return AgentName }

func (Adapter) Root() string { return SessionsDir() }

// SessionDir is the root itself: sessions are not stored per repository, so
// there is no narrower directory to watch.
func (Adapter) SessionDir(string) string { return SessionsDir() }

func (Adapter) SessionFiles(cwd string) ([]string, error) { return SessionFiles(cwd) }

func (Adapter) ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	return ParseSince(path, since)
}

func (Adapter) ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	return ParseSessionActivity(path, since)
}

func init() {
	adapter.RegisterLater(Adapter{})
	adapter.RegisterKeyed(AgentName, parseSince)
}
