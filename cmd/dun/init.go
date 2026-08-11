package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const hookMarker = "# managed-by: whodunit"

var trackedHooks = []string{"prepare-commit-msg", "commit-msg"}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Install AI-Attribution git hooks into this repository.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd)
		},
	}
}

func runInit(cmd *cobra.Command) error {
	gd, err := gitDir()
	if err != nil {
		return err
	}
	hooksDir := filepath.Join(gd, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve dun binary path: %w", err)
	}

	for _, hook := range trackedHooks {
		if err := installHook(hooksDir, hook, self); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", hook)
	}
	return nil
}

// installHook writes a hook script that chains to dun, then to any pre-existing
// hook of the same name so we never clobber the user's own hooks.
//
// The script prefers `dun` on PATH at hook-run time over the absolute path
// it was installed from — a binary built for one-off testing (a temp dir, a
// dev build) can move or vanish; PATH resolution survives that. dunPath is
// kept only as a fallback for the (less common) case where dun isn't on PATH.
func installHook(hooksDir, name, dunPath string) error {
	path := filepath.Join(hooksDir, name)
	chainPath := path + ".dun-chain"

	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Contains(existing, []byte(hookMarker)) {
			if err := os.WriteFile(chainPath, existing, 0o755); err != nil {
				return fmt.Errorf("preserve existing %s hook: %w", name, err)
			}
		}
	}

	script := fmt.Sprintf(
		"#!/bin/sh\n%s\nDUN=\"$(command -v dun || echo \"%s\")\"\n\"$DUN\" hook %s \"$@\"\nstatus=$?\nif [ -x \"%s\" ]; then \"%s\" \"$@\" || exit $?; fi\nexit $status\n",
		hookMarker, dunPath, name, chainPath, chainPath)

	return os.WriteFile(path, []byte(script), 0o755)
}
