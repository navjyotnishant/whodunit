// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 access to the journal's opt-in keyed line hashes.

// Package journal lets programs outside this module record and read keyed
// line hashes in dun's local journal, beside the unkeyed ones dun keeps.
//
// Only the opt-in keyed table is exposed. The rest of the journal stays
// internal to dun. The keyed table is created on the first AppendKeyedLines
// and never otherwise, so a journal used only by open-source dun keeps its
// schema. Purge and retention in dun cover keyed rows too.
//
// API stability: v0. Names may still change before whodunit 1.0.
package journal

import (
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/journal"
)

// DataDir is the directory dun keeps its journal in on this machine.
func DataDir() (string, error) { return config.DataDir() }

// KeyedWriter records keyed line hashes for one repository.
type KeyedWriter struct {
	w *journal.Writer
}

// OpenKeyed opens the journal in dataDir for one repository. repoID is the
// id dun uses for that repository.
func OpenKeyed(dataDir, repoID string) (*KeyedWriter, error) {
	w, err := journal.NewWriter(dataDir, repoID)
	if err != nil {
		return nil, err
	}
	return &KeyedWriter{w: w}, nil
}

// Append records hashes from linehash.OfKeyed under keyVersion. Recording a
// line already present is a no-op. A zero ts means now.
func (k *KeyedWriter) Append(keyVersion int, hashes []uint64, ts time.Time) error {
	return k.w.AppendKeyedLines(keyVersion, hashes, ts)
}

// Close releases the journal.
func (k *KeyedWriter) Close() error { return k.w.Close() }

// ReadKeyedLineHashes returns the keyed hashes recorded for a repository
// under one key version since a time. A journal with no keyed lines yields
// nil, and reading never creates the table.
func ReadKeyedLineHashes(dataDir, repoID string, keyVersion int, since time.Time) (map[uint64]struct{}, error) {
	return journal.ReadKeyedLineHashes(dataDir, repoID, keyVersion, since)
}
