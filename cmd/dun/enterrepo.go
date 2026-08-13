// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Resolving --repo for the commands that read the working
// directory rather than a repo id.

package main

import (
	"fmt"
	"os"
)

// enterRepo makes a command operate on the repository named by --repo, and
// explains itself when there is none.
//
// Some commands take a repo id and scope a query by it; `dun status` and
// `dun journal` work that way. Others read the working directory from
// several places at once — the repo id, the git log, the journal scope — and
// threading a path through all of them would touch far more code than
// changing directory does.
//
// The returned function restores the previous directory and must be
// deferred. It is never nil, so the caller does not need to check before
// deferring.
//
// The failure this exists for: run from outside a repository, these
// commands surfaced git's exit status —
//
//	Error: resolve repo root commit (unborn or not a git repo?): exit status 128
//
// which names neither the problem nor a way out. `dun status` and
// `dun journal` were fixed for exactly this; these were missed because they
// resolve the repository later, inside the work rather than at the entry.
//
// verb is what the command does with the repository ("publish", "report
// on"), used only in the message.
func enterRepo(repoFlag, name, verb string) (restore func(), err error) {
	noop := func() {}

	if repoFlag == "" {
		if inGitRepo("") {
			return noop, nil
		}
		return noop, notInRepoError(name, verb)
	}

	// Resolve first: this reports a missing directory or a path that is not
	// a repository in terms someone can act on, rather than letting the
	// chdir below fail with an errno.
	if _, _, err := resolveRepo(repoFlag); err != nil {
		return noop, err
	}

	prev, err := os.Getwd()
	if err != nil {
		return noop, err
	}
	if err := os.Chdir(repoFlag); err != nil {
		return noop, fmt.Errorf("--repo %s: %w", repoFlag, err)
	}
	return func() { os.Chdir(prev) }, nil
}

// notInRepoError says what is missing and the two commands that resolve it.
//
// Deliberately not a list of every instrumented repository. That was tried
// and reads as a wall on a machine with more than a handful — the reader
// wants the shape of the fix, and `dun repos list` is one keystroke away
// when they want the paths.
func notInRepoError(name, verb string) error {
	return fmt.Errorf(
		"not inside a git repository\n"+
			"%s works on one repository, so it needs to know which:\n"+
			"  %-26s %s a specific repository\n"+
			"  %-26s the full path of every repository, ready to paste",
		name,
		"dun "+name+" --repo <path>", verb,
		"dun repos list")
}
