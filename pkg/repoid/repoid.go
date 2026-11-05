// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Public v0 access to dun's repository identifier.

// Package repoid gives programs outside this module the identifier dun uses
// for a repository, so their records join with dun's journal and its
// DevLake sync on the same id.
//
// API stability: v0. Names may still change before whodunit 1.0.
package repoid

import "github.com/navjyotnishant/whodunit/internal/repoid"

// ForRepo returns dun's identifier for the repository at dir ("" means the
// current directory). It reads the repository and never writes to it.
func ForRepo(dir string) (string, error) { return repoid.ForRepo(dir) }
