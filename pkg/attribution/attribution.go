// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 wrapper over the intersection in internal/attribution.

// Package attribution exposes the line intersection at the heart of
// whodunit's attribution, for programs outside this module.
//
// API stability: v0. Names may still change before whodunit 1.0.
package attribution

import "github.com/navjyotnishant/whodunit/internal/attribution"

// Intersect counts the distinct staged line hashes that an agent produced.
// A count above zero is what makes a commit's method "intersected". Both
// sides must use the same hash scheme (see pkg/linehash).
func Intersect(staged []uint64, agent map[uint64]struct{}) int {
	return attribution.Intersect(staged, agent)
}
