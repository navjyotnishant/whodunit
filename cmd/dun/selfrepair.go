// Author: Navjyot Nishant
// Created: 2026-08-15
// Last updated: 2026-08-15
// Description: Repairing stale or missing hooks on the next dun command in
// a repository.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// repairHooks brings a repository's hooks up to date and says so once.
//
// The common case should need no command at all (NAV-76). Upgrading the
// binary reaches every repository for free — the hooks resolve `dun` from
// PATH at run time — but two kinds of change do not propagate: a hook that
// did not exist when the repository was instrumented, and a change to the
// hook script's own shape. Both have happened. `pre-push` was added after
// some repositories were already instrumented, and those simply never
// synced, silently, with nothing to indicate anything was missing.
//
// So the repair happens where someone is already looking, rather than
// waiting for them to run a command they have no reason to suspect they
// need.
//
// **Deliberately not called from the hooks themselves.** A commit hook is
// on the critical path of every commit, and a hook that rewrites hooks
// mid-commit is both slow and surprising — it would also be rewriting the
// script that is currently executing. Detection belongs where a person is
// reading output.
//
// Reports one line when it repairs something and nothing at all when
// there is nothing to do, because a notice on every command is noise that
// trains people to skip the output (criterion 7).
//
// A failure is reported and swallowed: `dun status` exists to answer a
// question, and failing it because a hook could not be rewritten would
// deny the answer over a side effect.
func repairHooks(w io.Writer, c *termcolor.Writer, repoPath string) {
	gitDir, err := gitDirFor(repoPath)
	if err != nil {
		return // not a repository, or unreadable: nothing to repair
	}

	missing, stale := staleHooks(gitDir)
	if len(missing) == 0 && len(stale) == 0 {
		return
	}

	// The binary doing the repairing, recorded as the hook's fallback path
	// exactly as `dun init` does.
	self, err := os.Executable()
	if err != nil {
		return
	}

	for _, hook := range trackedHooks {
		// installHook preserves a pre-existing non-whodunit hook by
		// chaining to it, which is the property that makes an automatic
		// repair safe at all (criterion 9). An unattended repair that ate
		// someone's husky hook would be far worse than staleness.
		if err := installHook(gitDir+"/hooks", hook, self); err != nil {
			fmt.Fprintf(w, "%s\n", c.S(termcolor.Warn,
				fmt.Sprintf("could not update hooks: %v", err)))
			return
		}
	}

	switch {
	case len(missing) > 0 && len(stale) > 0:
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, fmt.Sprintf(
			"updated hooks: %d added, %d refreshed", len(missing), len(stale))))
	case len(missing) > 0:
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, fmt.Sprintf(
			"installed %d hook(s) added since this repository was instrumented",
			len(missing))))
	default:
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, fmt.Sprintf(
			"refreshed %d hook(s) written by an older version", len(stale))))
	}
}
