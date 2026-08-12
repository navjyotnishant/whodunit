package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
	"github.com/spf13/cobra"
)

const hookMarker = "# managed-by: whodunit"

var trackedHooks = []string{"prepare-commit-msg", "commit-msg"}

func newInitCmd() *cobra.Command {
	var repoPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install AI-Attribution git hooks into a repository.",
		Long: "Installs the prepare-commit-msg and commit-msg hooks into a repository\n" +
			"and records it as instrumented.\n\n" +
			"Instrumentation is per repository and always explicit. There is no\n" +
			"flag to enrol every repository you have used an agent in: that set\n" +
			"includes client work, throwaway experiments, and clones of other\n" +
			"people's projects, and stamping attribution trailers into those is a\n" +
			"decision that belongs to you, one repository at a time. Run\n" +
			"`dun repos` to see candidates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, repoPath)
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "repository to instrument (default: current directory)")
	return cmd
}

func runInit(cmd *cobra.Command, repoPath string) error {
	if repoPath != "" {
		abs, err := filepath.Abs(repoPath)
		if err != nil {
			return fmt.Errorf("resolve --repo path: %w", err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return fmt.Errorf("--repo %s is not a directory", repoPath)
		}
		repoPath = abs
	}

	gd, err := gitDirFor(repoPath)
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

	// Report which agents were found, while the user is still watching.
	//
	// Installing hooks is only half of working: if no agent's transcripts
	// can be found, every commit is stamped undetermined and that reads as
	// "no AI was used" rather than "nothing was looked at" (NAV-21). Saying
	// so here turns a silent dead end into something fixable, before the
	// user walks away.
	//
	// Deliberately after the hooks are installed and never fatal: probing
	// is a report, not a gate.
	reportAgents(cmd.OutOrStdout(), repoPath)

	// Record the repository so anything working across repos — a daemon,
	// a cross-repo report — has an explicit list rather than discovering
	// repositories nobody opted in.
	//
	// A repository with no commits yet has no stable identifier, so it
	// cannot be registered. The hooks are still installed and will work;
	// only the registry entry waits until there is a root commit.
	repoID, idErr := repoid.ForRepo(repoPath)
	if idErr != nil {
		fmt.Fprintf(cmd.OutOrStdout(),
			"\nhooks are installed, but this repository has no commits yet so it\n"+
				"cannot be registered for cross-repo tooling. Re-run `dun init` after\n"+
				"the first commit.\n")
		return nil
	}

	recordPath := repoPath
	if recordPath == "" {
		if recordPath, err = os.Getwd(); err != nil {
			return err
		}
	}
	if err := registry.Add(repoID, recordPath, time.Now()); err != nil {
		return err
	}

	// Record who this journal belongs to, once per repository rather than
	// on every event. Central aggregation reads this instead of carrying
	// the same identity on every row.
	dataDir, err := journalDataDir()
	if err != nil {
		return err
	}
	contributor := contributorFor(repoPath)
	if err := journal.SetMetadata(dataDir, journal.Metadata{
		RepoID:      repoID,
		Contributor: contributor,
		UpdatedAt:   time.Now(),
	}); err != nil {
		return err
	}
	if contributor == "" {
		fmt.Fprintln(cmd.OutOrStdout(),
			"\nnote: git has no user.email configured here, so commits from this\n"+
				"repository will not carry a contributor identity.")
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
