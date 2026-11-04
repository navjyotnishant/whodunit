// Author: Navjyot Nishant
// Created: 2026-11-04
// Last updated: 2026-11-04
// Description: Public v0 access to the commit-time attribution step.

// Package trailer runs dun's commit-time attribution step for programs
// outside this module (spec 002 in whodunit-pro, task T002): it decides the
// AI-Attribution trailer for a commit and stamps it into the message, the
// same code dun's prepare-commit-msg and commit-msg hooks run.
//
// Env carries what the step needs from its caller. With a zero Env, the
// decision reads the staged change with git, transcripts through every
// supported agent, and recorded agent line hashes from the local journal,
// exactly as dun does; it only skips dun's journal ingest, hook log and
// replay log, which callers supply when they want them.
//
// API stability: v0. Names may still change before whodunit 1.0. The trailer
// format itself is versioned separately (v=2) and does not change here.
package trailer

import (
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/replaylog"
	"github.com/navjyotnishant/whodunit/internal/spec"
	itrailer "github.com/navjyotnishant/whodunit/internal/trailer"
)

// Trailer is a parsed AI-Attribution trailer. Format renders it.
type Trailer = spec.Trailer

// Env is what the step needs from its caller; see internal/trailer.Env.
type Env = itrailer.Env

// LogLevel is the level passed to Env.Log.
type LogLevel = hooklog.Level

// Levels passed to Env.Log.
const (
	LevelInfo = hooklog.LevelInfo
	LevelWarn = hooklog.LevelWarn
)

// ReplayEntry is what Env.Record receives for a determination worth retrying.
type ReplayEntry = replaylog.Entry

// Determine decides the trailer for the staged change and a commit message.
// It never fails: missing evidence degrades to a weaker method, never to a
// guess.
func Determine(message string, env Env) Trailer { return itrailer.Determine(message, env) }

// PrepareCommitMsg is the prepare-commit-msg hook: args are git's arguments
// (message file, source, commit). It never returns an error that should
// block a commit.
func PrepareCommitMsg(args []string, env Env) error { return itrailer.PrepareCommitMsg(args, env) }

// CommitMsg is the commit-msg hook: it rejects a message with more than one
// trailer or a malformed one.
func CommitMsg(args []string) error { return itrailer.CommitMsg(args) }

// StagedFiles lists the repo-relative paths staged in the current repository.
func StagedFiles() ([]string, error) { return itrailer.StagedFiles() }

// Lines returns the value of every AI-Attribution line in a message.
func Lines(msg string) []string { return itrailer.Lines(msg) }

// Parse parses a trailer value (the part after "AI-Attribution: ").
func Parse(value string) (Trailer, error) { return spec.Parse(value) }
