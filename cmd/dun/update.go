// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: `dun update` — upgrade through Homebrew, then refresh hooks.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Upgrade dun and refresh every repository's hooks.",
		Long: "Upgrades through Homebrew, then rewrites the hooks in every\n" +
			"instrumented repository so a newly added hook reaches them all.\n\n" +
			"dun never replaces its own binary. Homebrew installed it and\n" +
			"Homebrew upgrades it, so there is one installer, one uninstall\n" +
			"path, and no self-modifying executable.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd.OutOrStdout(), cmd.InOrStdin(), yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask before upgrading")
	return cmd
}

func runUpdate(w io.Writer, in io.Reader, yes bool) error {
	c := termcolor.New(w)

	// Homebrew must actually own this installation. Running `brew upgrade`
	// against a binary Homebrew did not install either does nothing or
	// installs a second copy alongside the one on PATH — and the user then
	// has two dun binaries and no idea which their hooks call.
	if !installedByHomebrew() {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Warn,
			"this dun was not installed by Homebrew, so `dun update` cannot upgrade it"))
		fmt.Fprintf(w, "\n  %s  %s\n", c.S(termcolor.Muted, "installed at"), currentBinary())
		fmt.Fprintln(w)
		fmt.Fprintln(w, "upgrade it the way you installed it, then run:")
		fmt.Fprintf(w, "  %s\n", c.S(termcolor.Bold, "dun repos update"))
		return nil
	}

	// A prompt with nothing to read from blocks forever. `dun update` will
	// be run from scripts and CI as readily as from a terminal, and a
	// command that hangs there is worse than one that refuses.
	if !yes && !isTerminal(in) {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Warn,
			"dun update needs a terminal to confirm, or --yes to skip the question"))
		return nil
	}
	if !yes {
		if !confirm(w, bufio.NewReader(in), "Upgrade dun through Homebrew?", true) {
			fmt.Fprintln(w, "not upgraded.")
			return nil
		}
	}

	fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, "upgrading via homebrew…"))
	brew := exec.Command("brew", "upgrade", "navjyotnishant/tap/dun")
	brew.Stdout = w
	brew.Stderr = w
	if err := brew.Run(); err != nil {
		// Hooks are deliberately left alone. Rewriting them to claim a
		// version that was not installed is precisely the drift this
		// command exists to remove.
		fmt.Fprintf(w, "\n%s %v\n", c.S(termcolor.Warn, "upgrade failed:"), err)
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"hooks were left unchanged — nothing is half-upgraded"))
		return err
	}

	fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted, "updating hooks…"))
	return runReposUpdate(w)
}

// installedByHomebrew reports whether the running binary lives under a
// Homebrew prefix.
//
// Checked by path rather than by asking brew: `brew list dun` succeeds even
// when a different binary shadows it earlier on PATH, which is the exact
// situation that makes an upgrade appear to work and change nothing.
func installedByHomebrew() bool {
	path := currentBinary()
	if path == "" {
		return false
	}
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return false
	}
	prefix := strings.TrimSpace(string(out))
	return prefix != "" && strings.HasPrefix(path, prefix)
}

// currentBinary returns the path of the running executable, resolving
// symlinks so a Homebrew shim reports its real location.
func currentBinary() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		return resolved
	}
	return self
}
