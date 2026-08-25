package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/agy"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	_ "github.com/navjyotnishant/whodunit/internal/adapter/codex"
	"github.com/navjyotnishant/whodunit/internal/attribution"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/declared"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/replaylog"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/spf13/cobra"
)

// The hook names as git invokes them, and as they appear in the log.
const (
	hookPrepare = "prepare-commit-msg"
	hookCommit  = "commit-msg"
	hookPrePush = "pre-push"
)

// hookProbe is a test seam, nil in every shipped binary.
var hookProbe func(hook string)

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <name> [args...]",
		Short:  "Internal: runs as a git hook (installed by dun init).",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			// The panic barrier, at the one place every hook passes through.
			//
			// Go has no exceptions, and every error below is returned and
			// handled explicitly — but a runtime panic is not an error. A
			// nil map write, an index out of range or a bad type assertion
			// anywhere in this path would take down the process mid-commit,
			// and "a commit never fails because attribution failed" does not
			// survive that.
			//
			// Recovering silently would be worse than crashing: it trades a
			// visible failure for an invisible no-op. The panic and its
			// stack go to the log, which is what makes recovery honest.
			defer func() {
				if r := recover(); r != nil {
					logPanic(args[0], r, debug.Stack())
					err = nil
				}
			}()

			// hookProbe is nil in a shipped binary. The barrier's own test
			// sets it, because a recover() no test can trigger is a
			// recover() nobody knows is broken — and a panicking code path
			// compiled into everyone's binary to prove it is worse.
			if hookProbe != nil {
				hookProbe(args[0])
			}

			switch args[0] {
			case hookPrepare:
				return runPrepareCommitMsg(args[1:])
			case hookCommit:
				return runCommitMsg(args[1:])
			case hookPrePush:
				// stderr, not stdout: git captures a pre-push hook's stdout
				// rather than showing it, so a warning written there is
				// invisible at exactly the moment someone needs to read it.
				return runPrePush(cmd.ErrOrStderr())
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

	// The message is read as well as written: an agent that leaves no
	// local transcript may still have declared itself in a trailer of its
	// own, and that is the only evidence such a commit carries.
	existing, _ := os.ReadFile(msgFile)

	trailer := determineTrailer(string(existing))

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
func determineTrailer(message string) spec.Trailer {
	now := time.Now()

	// Read first so it survives every early return below. A repository
	// with no transcript, no staged files or no journal still has whatever
	// the agent wrote into the message, and losing that to an early exit
	// would report undetermined on a commit that plainly said otherwise.
	fromDeclaration := attribution.FromDeclaration(declared.Parse(message))

	staged, err := stagedFiles()
	if err != nil {
		logHook(hookPrepare, hooklog.LevelWarn, "determine",
			"cannot list staged files: "+err.Error())
		return fromDeclaration
	}
	if len(staged) == 0 {
		logHook(hookPrepare, hooklog.LevelInfo, "determine",
			"no staged files")
		return fromDeclaration
	}

	cwd, err := os.Getwd()
	if err != nil {
		logHook(hookPrepare, hooklog.LevelWarn, "determine",
			"cannot resolve the working directory: "+err.Error())
		return fromDeclaration
	}
	// Journal entries carry absolute paths; git gives repo-relative ones.
	//
	// Resolved once, not per file: the journal records the directory the
	// agent resolved to, and os.Getwd returns whatever the shell was in.
	// One location can have several names — /tmp against /private/tmp, and
	// on Windows the 8.3 short form C:\Users\RUNNER~1\… against the long
	// one — and these paths are compared as exact strings, so two spellings
	// mean nothing matches and the commit is stamped undetermined.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	for i, f := range staged {
		staged[i] = filepath.Join(cwd, f)
	}
	// The same window attribution.Determine applies, not a second copy of
	// the number. These two have always had to agree and nothing enforced
	// it.
	since := now.Add(-attribution.LookbackWindow)

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
	// A config that will not parse discards every agent path override and
	// falls back to the built-in defaults. That is the right fallback, but
	// it is invisible: attribution simply stops finding transcripts, every
	// commit is stamped undetermined, and it reads as "no AI was used".
	//
	// Said out loud here because this is the path that never loads the
	// config directly and so never had anywhere to report it.
	if err := adapter.ConfigError(); err != nil {
		logHook(hookPrepare, hooklog.LevelWarn, "config",
			"config.json could not be read, so any agent paths it sets are "+
				"being ignored: "+err.Error())
	}

	if _, _, err := ingestSince(since, func(path string, perr error) {
		logHook(hookPrepare, hooklog.LevelWarn, "ingest",
			"could not read a transcript: "+perr.Error()+" ("+filepath.Base(path)+")")
	}); err != nil {
		logHook(hookPrepare, hooklog.LevelWarn, "ingest", err.Error())
	}

	var entries []journal.Entry
	for _, ad := range adapter.All() {
		sessionPaths, err := ad.SessionFiles(cwd)
		if err != nil {
			// An agent we cannot look at is not evidence of absence — but
			// it is the difference between "no AI was used" and "we could
			// not tell", which is the whole of NAV-21.
			logHook(hookPrepare, hooklog.LevelWarn, "determine",
				"cannot list "+ad.Name()+" sessions: "+err.Error())
			continue
		}
		for _, p := range sessionPaths {
			parsed, err := ad.ParseSince(p, since)
			if err != nil {
				logHook(hookPrepare, hooklog.LevelWarn, "determine",
					"unreadable "+ad.Name()+" transcript "+filepath.Base(p)+": "+err.Error())
				continue // one bad transcript doesn't block the whole determination
			}
			entries = append(entries, parsed...)
		}
	}
	if len(entries) == 0 {
		logHook(hookPrepare, hooklog.LevelInfo, "determine",
			fmt.Sprintf("no agent activity found in the last %d days", lookbackDays))
		// No transcript from any agent, and the adapters were readable -
		// the warns above fire when they are not. So the tooling was
		// watching and there was nothing to see: a human wrote this
		// (WHO-211).
		//
		// Only when nothing declared itself either. A declaration is
		// evidence an agent was involved, and it outranks this.
		if fromDeclaration.Status == spec.StatusUndetermined {
			return spec.WithStatus(spec.StatusUnassisted)
		}
		return fromDeclaration
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

	// Both candidates, resolved by which rests on stronger evidence
	// rather than by which was computed first. Transcript evidence wins
	// because observed and intersected outrank declared on the ladder, not
	// because it is checked first - so a future producer yielding
	// something stronger needs no change here.
	trailer := attribution.Best(
		attribution.Determine(entries, staged, agentLines,
			attribution.StagedEvidence{
				Lines:  lines,
				Commit: attribution.CommitLines{Added: added, Removed: removed},
			}, now),
		fromDeclaration)

	// A determination that failed is worth keeping, so a later run can
	// try again (WHO-212). Only the two failure statuses reach the log;
	// Record drops the rest.
	//
	// No commit SHA here - prepare-commit-msg runs before the commit
	// exists - so the entry carries what a replay actually needs: the
	// staged files to match against, and how many agent lines there were
	// to match with.
	if home, herr := config.Dir(); herr == nil {
		repoID, _ := currentRepoID()
		replaylog.Record(home, replaylog.Entry{
			RepoID:      repoID,
			Time:        now,
			Status:      trailer.Status,
			StagedFiles: staged,
			AgentLines:  len(agentLines),
		})
	}

	// The commit carries a derived token, never the agent's own session id.
	//
	// That id is the transcript filename on disk, so stamping it verbatim
	// would record a pointer into a file holding every prompt of the
	// session — permanently, in a message that gets pushed. The token still
	// groups commits from one working period, which is all the trailer
	// needs it for (NAV-7).
	//
	// Done here rather than inside Determine because the repo id lives on
	// this side, and hashing without it would let the same session be
	// correlated across repositories.
	if trailer.Session != "" {
		if repoID, err := currentRepoID(); err == nil {
			trailer.Session = spec.SessionToken(repoID, trailer.Session)
		} else {
			// No repo id means no repo-scoped token. Hash anyway rather
			// than fall back to the raw id: an unscoped token is weaker
			// than intended, a leaked filename is worse.
			trailer.Session = spec.SessionToken("", trailer.Session)
		}
	}

	// The outcome, not just the failures. "Did the hook run at all" is one
	// of the questions this log exists to answer, and a log that speaks
	// only when something breaks cannot answer it.
	logHook(hookPrepare, hooklog.LevelInfo, "determine",
		fmt.Sprintf("%s via %s, %d staged file(s), %d agent line(s)",
			trailer.Status, trailer.Method, len(staged), len(agentLines)))
	return trailer
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
