// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: `dun repos update` — bring every instrumented repository's
// hooks up to date after an upgrade.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newReposUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Reinstall hooks in every instrumented repository.",
		Long: "Upgrading dun reaches every repository automatically for changes\n" +
			"inside the binary: the hooks resolve `dun` from PATH each time they\n" +
			"run, so a fix lands everywhere at once.\n\n" +
			"What does not propagate is a hook that did not exist when a\n" +
			"repository was instrumented, or a change to the hook script itself.\n" +
			"Those need rewriting, and this rewrites them everywhere rather than\n" +
			"asking you to visit each repository in turn.\n\n" +
			"A pre-existing hook that whodunit did not write is preserved and\n" +
			"chained, exactly as `dun init` does.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReposUpdate(cmd.OutOrStdout())
		},
	}
}

func runReposUpdate(w io.Writer) error {
	c := termcolor.New(w)

	entries, err := registry.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "no repositories are instrumented yet.")
		fmt.Fprintf(w, "\ninstrument one with:  %s\n", c.S(termcolor.Bold, "dun init"))
		return nil
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve dun binary path: %w", err)
	}

	var updated, skipped, failed int
	for _, e := range entries {
		name := shortRepoName(e.Path)

		// A repository can move or be deleted after init recorded it.
		// Skipping it is right; failing the whole command over one absent
		// directory would leave every later repository un-updated.
		if !inGitRepo(e.Path) {
			fmt.Fprintf(w, "  %s  %-30s %s\n", marker(c, levelInfo), name,
				c.S(termcolor.Muted, "moved or deleted, skipped"))
			skipped++
			continue
		}

		gitDir, err := gitDirFor(e.Path)
		if err != nil {
			fmt.Fprintf(w, "  %s  %-30s %s\n", marker(c, levelBroken), name,
				c.S(termcolor.Warn, err.Error()))
			failed++
			continue
		}

		missing, stale := staleHooks(gitDir)
		if len(missing) == 0 && len(stale) == 0 {
			fmt.Fprintf(w, "  %s  %-30s %s\n", marker(c, levelOK), name,
				c.S(termcolor.Muted, "already current"))
			continue
		}

		var hookErr error
		for _, hook := range trackedHooks {
			if err := installHook(gitDir+"/hooks", hook, self); err != nil {
				hookErr = err
				break
			}
		}
		if hookErr != nil {
			fmt.Fprintf(w, "  %s  %-30s %s\n", marker(c, levelBroken), name,
				c.S(termcolor.Warn, hookErr.Error()))
			failed++
			continue
		}

		detail := "updated"
		if len(missing) > 0 {
			detail = "installed " + joinNames(missing)
		}
		fmt.Fprintf(w, "  %s  %-30s %s\n", marker(c, levelOK), name,
			c.S(termcolor.Good, detail))
		updated++
	}

	fmt.Fprintln(w)
	switch {
	case failed > 0:
		return fmt.Errorf("%d repository(ies) could not be updated", failed)
	case updated > 0:
		fmt.Fprintf(w, "%s %s\n", c.S(termcolor.Good, "✔"),
			c.S(termcolor.Good, fmt.Sprintf("%d repository(ies) updated", updated)))
	default:
		fmt.Fprintf(w, "%s %s\n", c.S(termcolor.Good, "✔"),
			c.S(termcolor.Good, "every repository is already current"))
	}
	if skipped > 0 {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			fmt.Sprintf("%d skipped — moved or deleted", skipped)))
	}
	return nil
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return fmt.Sprintf("%d hooks", len(names))
	}
}
