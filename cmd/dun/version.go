// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The version this binary was built as, and the `dun version`
// command.

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is stamped at build time by scripts/release.sh:
//
//	go build -ldflags "-X main.version=v0.2.0"
//
// The release script has always passed that flag, but no variable existed
// to receive it, so every release binary reported nothing. A build with no
// stamp says "dev" rather than inventing a number: claiming a release
// version for a local build is how someone ends up debugging a fix they
// never installed.
var version = "dev"

// Version returns what this binary was built as.
func Version() string { return version }

// IsRelease reports whether this is a stamped release build.
//
// Staleness checks and upgrade prompts need this: a dev build has no
// meaningful version to compare against, and telling someone their local
// build is out of date relative to a release is noise.
func IsRelease() bool { return version != "dev" && version != "" }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of dun.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}
