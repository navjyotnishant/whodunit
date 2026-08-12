// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Resolves where an agent's transcripts live, and reports
// whether that location actually exists.

package adapter

import (
	"os"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
)

// ResolveRoot returns the directory to look for an agent's transcripts in,
// and where that answer came from.
//
// Resolution order, highest first:
//
//  1. WHODUNIT_<AGENT>_PATH — one-off, for CI and debugging
//  2. the agents map in ~/.whodunit/config.json — persistent
//  3. builtin — the agent's own default, passed in by its package
//
// Resolution lives here rather than in each adapter because it is identical
// for every agent: only the default differs. Doing it per-adapter is how one
// of them ends up not honouring the override.
//
// An agent's own environment variable (CLAUDE_CONFIG_DIR) is handled inside
// that agent's package, since only it knows the variable exists. It applies
// to the builtin default passed in here, so an explicit whodunit override
// still wins.
func ResolveRoot(agent, builtin string) (path string, source Source) {
	if v := os.Getenv(EnvVarFor(agent)); v != "" {
		return v, SourceEnv
	}
	// A config that cannot be read must not silence an agent: fall through
	// to the default rather than returning nothing. A broken config file is
	// already reported by the commands that load it.
	if cfg, err := config.Load(); err == nil {
		if a, ok := cfg.Agents[agent]; ok && a.Path != "" {
			return a.Path, SourceConfig
		}
	}
	return builtin, SourceDefault
}

// Source is where a resolved path came from, so a report can say why it is
// looking somewhere and what to change.
type Source string

const (
	SourceEnv     Source = "environment"
	SourceConfig  Source = "config"
	SourceDefault Source = "default"
)

// EnvVarFor returns the environment variable that overrides one agent's
// path: claude-code -> WHODUNIT_CLAUDE_CODE_PATH.
func EnvVarFor(agent string) string {
	return "WHODUNIT_" + strings.ToUpper(strings.ReplaceAll(agent, "-", "_")) + "_PATH"
}

// State is what a detection probe found.
type State string

const (
	// StateFound means the directory exists and holds session records.
	StateFound State = "found"

	// StateEmpty means the directory exists but holds no sessions — the
	// agent is installed and has not been used for this repository.
	StateEmpty State = "empty"

	// StateNotInstalled means the default location is absent and nothing
	// was configured. Not an error: most machines have some agents and not
	// others, and saying otherwise nags people about tools they do not use.
	StateNotInstalled State = "not installed"

	// StateMissing means a path was explicitly configured and does not
	// exist. This one IS a mistake to fix, and is the reason the three
	// states are not collapsed into two.
	StateMissing State = "path not found"

	// StateError means the probe itself failed — unreadable directory, bad
	// permissions. Distinct from "nothing there", because absence of
	// evidence must never be reported as evidence of absence (NAV-21).
	StateError State = "unknown"
)

// Detection is the result of probing one agent.
type Detection struct {
	Agent string

	// Path is the per-repository session directory that was searched.
	Path string

	// Root is the agent's transcript root — the thing an override sets.
	// Reported separately because a user fixing a wrong location needs to
	// see the value they configured, not the repository subdirectory
	// derived from it.
	Root string

	Source   Source
	State    State
	Sessions int
	Err      error
}

// Detect probes every registered adapter for the repository at cwd.
//
// Deliberately cheap: it asks each adapter for its session files and counts
// them. No transcript is parsed, no database is opened, nothing is locked.
// This runs during `dun init`, where it must never slow down or fail the
// thing the user actually asked for.
func Detect(cwd string) []Detection {
	out := make([]Detection, 0, len(registered))
	for _, a := range registered {
		out = append(out, detectOne(a, cwd))
	}
	return out
}

func detectOne(a Adapter, cwd string) Detection {
	d := Detection{Agent: a.Name()}

	dir := a.SessionDir(cwd)
	d.Path = dir
	d.Root = a.Root()

	// An adapter that cannot name a directory has no default worth
	// reporting — treat it as absent rather than as an error.
	if dir == "" {
		d.State = StateNotInstalled
		return d
	}

	// Whether a path was chosen explicitly decides how a missing directory
	// reads: configured-and-absent is a mistake, default-and-absent is just
	// an agent this person does not use.
	_, src := ResolveRoot(a.Name(), a.Root())
	d.Source = src

	files, err := a.SessionFiles(cwd)
	if err != nil {
		d.State, d.Err = StateError, err
		return d
	}
	d.Sessions = len(files)

	switch {
	case len(files) > 0:
		d.State = StateFound
	case !dirExists(dir):
		if src == SourceDefault {
			d.State = StateNotInstalled
		} else {
			d.State = StateMissing
		}
	default:
		d.State = StateEmpty
	}
	return d
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
