package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	"github.com/navjyotnishant/whodunit/internal/attribution"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/spf13/cobra"
)

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <name> [args...]",
		Short:  "Internal: runs as a git hook (installed by dun init).",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "prepare-commit-msg":
				return runPrepareCommitMsg(args[1:])
			case "commit-msg":
				return runCommitMsg(args[1:])
			default:
				return nil // unknown hook name: no-op, never block the commit
			}
		},
	}
}

// runPrepareCommitMsg stamps an AI-Attribution trailer into the commit message
// file. Per spec: must never block or fail the commit — on any error, stamp
// undetermined and exit 0.
func runPrepareCommitMsg(args []string) error {
	if len(args) == 0 {
		return nil
	}
	msgFile := args[0]

	trailer := determineTrailer()

	f, err := os.OpenFile(msgFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil // never fail the commit over a stamping error
	}
	defer f.Close()
	fmt.Fprintf(f, "\n%s\n", trailer.Format())
	return nil
}

// determineTrailer picks the highest-confidence method available by checking
// staged files against the Claude Code session transcript for this repo.
// Any failure along the way (git not available, no transcript found, no
// coverage) degrades to undetermined rather than guessing (NAV-21).
func determineTrailer() spec.Trailer {
	now := time.Now()

	staged, err := stagedFiles()
	if err != nil || len(staged) == 0 {
		return spec.Undetermined()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return spec.Undetermined()
	}
	// Journal entries carry absolute paths; git gives repo-relative ones.
	for i, f := range staged {
		staged[i] = filepath.Join(cwd, f)
	}
	sessionPaths, err := claudecode.SessionFiles(cwd)
	if err != nil || len(sessionPaths) == 0 {
		return spec.Undetermined()
	}

	since := now.Add(-7 * 24 * time.Hour)
	var entries []journal.Entry
	for _, p := range sessionPaths {
		parsed, err := claudecode.ParseSince(p, since)
		if err != nil {
			continue // one bad transcript doesn't block the whole determination
		}
		entries = append(entries, parsed...)
	}

	// A failed hunk-hash lookup just means we can't upgrade to intersected —
	// Determine still returns a valid observed-or-undetermined result with nil.
	hunkHashes, _ := attribution.StagedHunkHashes()

	// Likewise for line counts: without them the ratio is simply omitted,
	// which is the honest outcome rather than a guessed number.
	added, removed, _ := attribution.StagedLineCounts()

	return attribution.Determine(entries, staged, hunkHashes,
		attribution.CommitLines{Added: added, Removed: removed}, now)
}

// stagedFiles returns repo-relative paths of files staged for this commit.
func stagedFiles() ([]string, error) {
	out, err := exec.Command("git", "diff", "--cached", "--name-only").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// runCommitMsg validates that the commit message carries exactly one
// well-formed AI-Attribution trailer. Rejects malformed trailers; a missing
// trailer is not this hook's concern (that's what prepare-commit-msg stamps).
func runCommitMsg(args []string) error {
	if len(args) == 0 {
		return nil
	}
	msgFile := args[0]

	f, err := os.Open(msgFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	var matches []string
	scanner := bufio.NewScanner(f)
	prefix := spec.TrailerKey + ":"
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, prefix) {
			matches = append(matches, strings.TrimSpace(line[len(prefix):]))
		}
	}

	if len(matches) == 0 {
		return nil // no trailer present: not this hook's problem
	}
	if len(matches) > 1 {
		return fmt.Errorf("commit message has %d %s trailers, exactly one is required", len(matches), spec.TrailerKey)
	}
	if _, err := spec.Parse(matches[0]); err != nil {
		return fmt.Errorf("invalid %s trailer: %w", spec.TrailerKey, err)
	}
	return nil
}
