// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Registers Claude Code with the adapter registry.

package claudecode

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Adapter exposes this package through the adapter.Adapter contract.
//
// The package-level functions stay exported: they are the ones with the
// format knowledge, and they are what the tests exercise directly. This
// type is the thin binding that lets commands iterate agents rather than
// naming one.
type Adapter struct{}

func (Adapter) Name() string { return AgentName }

func (Adapter) SessionDir(cwd string) string { return SessionDir(cwd) }

func (Adapter) Root() string { return ProjectsDir() }

func (Adapter) SessionFiles(cwd string) ([]string, error) { return SessionFiles(cwd) }

func (Adapter) ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	return ParseSince(path, since)
}

func (Adapter) ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	return ParseSessionActivity(path, since)
}

func init() { adapter.Register(Adapter{}) }
