// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Whether the race detector is instrumenting this binary.

package testmode

// RaceEnabled reports whether this binary was built with -race.
//
// Timing assertions must not run under it. The detector adds roughly an
// order of magnitude of overhead, so a wall-clock budget measures the
// instrumentation rather than the code — a real regression and a race build
// look identical, and the gate fails for a reason nobody can act on.
//
// The correctness half of these tests still runs; only the stopwatch is
// skipped.
var RaceEnabled = raceEnabled
