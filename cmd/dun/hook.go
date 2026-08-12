package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/agy"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
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
	since := now.Add(-7 * 24 * time.Hour)

	// Record what the agents did before deciding what to stamp.
	//
	// Commit time is when the evidence is needed, so it is also when it is
	// written — no background daemon, and no `dun ingest` a user has to
	// remember. Without this the journal stays empty on a normal install,
	// and the line-hash lookup below finds nothing, capping every commit at
	// observed no matter how much the agent actually wrote.
	//
	// Failure here is deliberately ignored: a full journal makes the
	// attribution better, an empty one makes it weaker, and neither is a
	// reason to fail someone's commit.
	_, _, _ = ingestSince(since, func(string, error) {})

	var entries []journal.Entry
	for _, ad := range adapter.All() {
		sessionPaths, err := ad.SessionFiles(cwd)
		if err != nil {
			continue // an agent we cannot look at is not evidence of absence
		}
		for _, p := range sessionPaths {
			parsed, err := ad.ParseSince(p, since)
			if err != nil {
				continue // one bad transcript doesn't block the whole determination
			}
			entries = append(entries, parsed...)
		}
	}
	if len(entries) == 0 {
		return spec.Undetermined()
	}

	// Line hashes come from two places, and both matter.
	//
	// The journal accumulates across sessions and deduplicates, so a line
	// written weeks ago in a transcript since deleted still matches. The
	// entries just parsed carry hashes the journal may not have yet — a
	// session still being written, or a first commit on a machine where
	// the ingest above failed.
	//
	// Reading only the journal was the original bug: the hook held the
	// hashes it needed and queried an empty database instead.
	agentLines := agentLineHashes(since)
	if agentLines == nil {
		agentLines = map[uint64]struct{}{}
	}
	for _, e := range entries {
		for _, h := range e.LineHashes {
			agentLines[h] = struct{}{}
		}
	}

	lines, _ := attribution.StagedLines()
	added, removed, _ := attribution.StagedLineCounts()

	return attribution.Determine(entries, staged, agentLines,
		attribution.StagedEvidence{
			Lines:  lines,
			Commit: attribution.CommitLines{Added: added, Removed: removed},
		}, now)
}

// agentLineHashes loads this repository's recorded agent line hashes.
// Returns nil on any failure — the hook must never block a commit, and a
// missing lookup degrades the determination rather than failing it.
func agentLineHashes(since time.Time) map[uint64]struct{} {
	dataDir, err := journalDataDir()
	if err != nil {
		return nil
	}
	repoID, err := currentRepoID()
	if err != nil {
		return nil
	}
	hashes, err := journal.ReadLineHashes(dataDir, repoID, since)
	if err != nil {
		return nil
	}
	return hashes
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
