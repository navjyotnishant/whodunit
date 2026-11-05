// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Registers Desktop Commander with the adapter registry.

package desktopcommander

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter exposes this package through the adapter.Adapter contract.
type Adapter struct{}

func (Adapter) Name() string { return AgentName }

func (Adapter) Root() string { return Dir() }

// SessionDir is the server's directory: one history covers every
// repository, so there is no per-repository directory.
func (Adapter) SessionDir(string) string { return Dir() }

func (Adapter) SessionFiles(cwd string) ([]string, error) { return SessionFiles(cwd) }

func (Adapter) ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	return ParseSince(path, since)
}

func (Adapter) ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	return ParseSessionActivity(path, since)
}

func init() { adapter.Register(Adapter{}) }
