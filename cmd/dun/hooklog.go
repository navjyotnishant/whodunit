// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: What the hooks record about their own runs, and the panic
// barrier's report.

package main

import (
	"fmt"
	"os"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
)

// logHook records one hook action.
//
// Resolving the repository here rather than at each call site keeps the
// callers to one line, which matters: a logging call that is awkward to
// write is a logging call that does not get written, and the swallowed
// errors this exists to surface are spread across a dozen places.
func logHook(hook string, level hooklog.Level, event, detail string) {
	home, err := config.Dir()
	if err != nil {
		return
	}

	e := hooklog.Entry{
		Level:  level,
		Hook:   hook,
		Event:  event,
		Detail: detail,
	}
	if cwd, err := os.Getwd(); err == nil {
		e.Repo = cwd
	}
	if id, err := currentRepoID(); err == nil {
		e.RepoID = id
	}

	hooklog.Write(home, e)
}

// logPanic records a recovered panic, with the stack that produced it.
//
// The stack is the whole value here. A panic recorded as "something went
// wrong" leaves the reader exactly where the crash did, and this is the one
// entry nobody can reproduce on demand.
func logPanic(hook string, r any, stack []byte) {
	home, err := config.Dir()
	if err != nil {
		return
	}

	e := hooklog.Entry{
		Level:  hooklog.LevelPanic,
		Hook:   hook,
		Event:  "panic",
		Detail: fmt.Sprint(r),
		Stack:  string(stack),
	}
	if cwd, err := os.Getwd(); err == nil {
		e.Repo = cwd
	}
	if id, err := currentRepoID(); err == nil {
		e.RepoID = id
	}

	hooklog.Write(home, e)
}
