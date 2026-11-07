// Author: Navjyot Nishant
// Created: 2026-11-07
// Last updated: 2026-11-07
// Description: git commands with a time limit and no prompts.

// Package gitexec builds git commands that cannot hang forever.
//
// A git command that never returns stalls whatever is waiting on it: a
// commit hook, or a long-running service built on the public packages. On
// 2026-11-06 a `git rev-parse --show-toplevel` hung for over 90 minutes in
// one repository and stopped a background collector. Commands built here
// stop after Limit, never ask for input on a terminal, and take no optional
// locks. A command that finishes in time behaves exactly as before.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// Limit is how long one git command may run. It is generous: a normal
// command takes milliseconds, and a large log a few seconds.
var Limit = 2 * time.Minute

// Command returns git with args, bound to Limit. Call the returned cancel
// when the command has finished.
func Command(bin string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), Limit)
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- callers pass fixed git subcommands
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 5 * time.Second
	return cmd, cancel
}

// Output runs git with args in dir ("" for the current directory) and
// returns its standard output.
func Output(bin, dir string, args ...string) ([]byte, error) {
	cmd, cancel := Command(bin, args...)
	defer cancel()
	cmd.Dir = dir
	return cmd.Output()
}
