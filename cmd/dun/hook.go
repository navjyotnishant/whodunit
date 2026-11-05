package main

import (
	"path/filepath"
	"runtime/debug"
	"time"

	_ "github.com/navjyotnishant/whodunit/internal/adapter/agy"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudedesktop"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/desktopcommander"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/replaylog"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/navjyotnishant/whodunit/internal/trailer"
	"github.com/spf13/cobra"
)

// The hook names as git invokes them, and as they appear in the log.
const (
	hookPrepare = "prepare-commit-msg"
	hookCommit  = "commit-msg"
	hookPrePush = "pre-push"
)

// hookProbe is a test seam, nil in every shipped binary.
var hookProbe func(hook string)

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <name> [args...]",
		Short:  "Internal: runs as a git hook (installed by dun init).",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			// The panic barrier, at the one place every hook passes through.
			//
			// Go has no exceptions, and every error below is returned and
			// handled explicitly — but a runtime panic is not an error. A
			// nil map write, an index out of range or a bad type assertion
			// anywhere in this path would take down the process mid-commit,
			// and "a commit never fails because attribution failed" does not
			// survive that.
			//
			// Recovering silently would be worse than crashing: it trades a
			// visible failure for an invisible no-op. The panic and its
			// stack go to the log, which is what makes recovery honest.
			defer func() {
				if r := recover(); r != nil {
					logPanic(args[0], r, debug.Stack())
					err = nil
				}
			}()

			// hookProbe is nil in a shipped binary. The barrier's own test
			// sets it, because a recover() no test can trigger is a
			// recover() nobody knows is broken — and a panicking code path
			// compiled into everyone's binary to prove it is worse.
			if hookProbe != nil {
				hookProbe(args[0])
			}

			switch args[0] {
			case hookPrepare:
				return runPrepareCommitMsg(args[1:])
			case hookCommit:
				return runCommitMsg(args[1:])
			case hookPrePush:
				// stderr, not stdout: git captures a pre-push hook's stdout
				// rather than showing it, so a warning written there is
				// invisible at exactly the moment someone needs to read it.
				return runPrePush(cmd.ErrOrStderr())
			default:
				return nil // unknown hook name: no-op, never block the commit
			}
		},
	}
}

// hookEnv is what dun's commit-time attribution step runs with: its hook log,
// its journal ingest and its replay log. Everything else is the trailer
// package's default, which is what dun has always done.
func hookEnv() trailer.Env {
	return trailer.Env{
		Log: func(level hooklog.Level, event, detail string) {
			logHook(hookPrepare, level, event, detail)
		},
		Ingest: func(since time.Time) error {
			_, _, err := ingestSince(since, func(path string, perr error) {
				logHook(hookPrepare, hooklog.LevelWarn, "ingest",
					"could not read a transcript: "+perr.Error()+" ("+filepath.Base(path)+")")
			})
			return err
		},
		Record: func(e replaylog.Entry) {
			if home, err := config.Dir(); err == nil {
				replaylog.Record(home, e)
			}
		},
	}
}

// runPrepareCommitMsg stamps the AI-Attribution trailer (internal/trailer).
func runPrepareCommitMsg(args []string) error { return trailer.PrepareCommitMsg(args, hookEnv()) }

// determineTrailer decides the trailer for a commit message (internal/trailer).
func determineTrailer(message string) spec.Trailer { return trailer.Determine(message, hookEnv()) }

// runCommitMsg checks the message carries at most one valid trailer.
func runCommitMsg(args []string) error { return trailer.CommitMsg(args) }

// stagedFiles returns repo-relative paths of files staged for this commit.
func stagedFiles() ([]string, error) { return trailer.StagedFiles() }

// trailerLines returns the value of every AI-Attribution line in a message.
func trailerLines(msg string) []string { return trailer.Lines(msg) }
