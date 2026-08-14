package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
	"github.com/spf13/cobra"
)

const hookMarker = "# managed-by: whodunit"

// hookVersionMarker prefixes the line recording which version wrote a hook.
const hookVersionMarker = "# whodunit-version:"

// trackedHooks are the git hooks dun installs.
//
// pre-push publishes to a shared database when one is configured, and does
// nothing at all when one is not — so installing it unconditionally costs
// an unconfigured user nothing but saves them a second `dun init` later.
var trackedHooks = []string{"prepare-commit-msg", "commit-msg", "pre-push"}

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

	// Capture the pre-adoption baseline before anything else happens.
	//
	// This has to be first, and it only gets one chance. The moment the
	// hooks below start stamping commits, the unassisted window is over —
	// and every before/after comparison needs a measurement from before.
	// `dun baseline capture` has always existed and its help has always
	// said "run this FIRST", which turns out to be the same as not having
	// it: nobody runs a command they have not needed yet, and by the time
	// the comparison is wanted the window has closed for good.
	captureBaselineOnInit(cmd.OutOrStdout(), repoPath)

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

	// Report an existing publishing target, or offer to set one up once.
	// Skipped silently when stdin is not a terminal — init runs in CI and
	// in scripts, where a prompt is a hung build rather than a question.
	offerDatalakeSetup(cmd.OutOrStdout(), cmd.InOrStdin())

	// After the offer, not instead of it. Someone who just declined the
	// wizard has made a choice; this says what that choice costs, which the
	// wizard's own "skipped" message does not.
	if cfg, err := config.Load(); err == nil {
		warnLocalOnly(cmd.OutOrStdout(), cfg)
	}

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

	// The version that wrote this hook, recorded so staleness is
	// detectable. Without it nothing can distinguish a hook written by an
	// old release from a current one, and a repository sits on an
	// outdated hook set indefinitely with no signal — which is how a
	// repository ends up missing a hook added months earlier.
	script := fmt.Sprintf(
		"#!/bin/sh\n%s\n%s %s\nDUN=\"$(command -v dun || echo \"%s\")\"\n\"$DUN\" hook %s \"$@\"\nstatus=$?\nif [ -x \"%s\" ]; then \"%s\" \"$@\" || exit $?; fi\nexit $status\n",
		hookMarker, hookVersionMarker, version, dunPath, name, chainPath, chainPath)

	return os.WriteFile(path, []byte(script), 0o755)
}

// hookVersionOf returns the version of dun that wrote a hook, and whether
// it carries a stamp at all.
//
// A hook written before stamping existed has none, which is itself
// information: it predates this mechanism and is therefore stale.
func hookVersionOf(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, hookVersionMarker+" "); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// staleHooks returns the hooks in a repository that are missing or were
// written by a different version of dun.
//
// A dev build reports nothing stale: it has no meaningful version, and
// telling someone their hooks disagree with an unversioned local build is
// noise rather than a finding.
func staleHooks(gitDir string) (missing, stale []string) {
	for _, name := range trackedHooks {
		path := filepath.Join(gitDir, "hooks", name)
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, name)
			continue
		}
		if !IsRelease() {
			continue
		}
		got, ok := hookVersionOf(path)
		if !ok || got != version {
			stale = append(stale, name)
		}
	}
	return missing, stale
}
