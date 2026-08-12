// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: `dun verify` — one command answering "is this working right now".

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

// level is how much a finding matters.
//
// The distinction between broken and unconfigured is the one that decides
// whether this command is useful or ignored. Local-only is a supported way
// to run whodunit — the setup wizard offers it — so an unconfigured sync
// must never read as a failure or affect the exit code.
type level int

const (
	levelOK      level = iota // working
	levelInfo                 // a fact, not a problem: an agent not installed
	levelUnknown              // could not be checked — no network, unreachable
	levelBroken               // genuinely wrong, and fixable
)

// finding is one check's result.
type finding struct {
	Area   string
	Level  level
	Detail string

	// Fix is the command that resolves it. A finding that names a problem
	// without naming its remedy has done half the job.
	Fix string
}

func newVerifyCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check that attribution is set up and actually working.",
		Long: "Runs every check whodunit can make and reports what is wrong,\n" +
			"with the command that fixes each thing.\n\n" +
			"Safe to run at any time: it reads, and writes nothing — not the\n" +
			"journal, not a commit, not the sync target.\n\n" +
			"Exit code is non-zero only when something is genuinely broken.\n" +
			"Parts you simply have not configured are reported as facts, so\n" +
			"this can gate a CI job without failing a local-only setup.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(cmd.OutOrStdout(), repoFlag)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to check (default: current directory)")
	return cmd
}

func runVerify(w io.Writer, repoFlag string) error {
	var findings []finding

	findings = append(findings, checkInstall()...)
	findings = append(findings, checkAgents(repoFlag)...)

	repoPath, inRepo := verifyRepoPath(repoFlag)
	if inRepo {
		findings = append(findings, checkHooks(repoPath)...)
		findings = append(findings, checkJournal(repoPath)...)
		findings = append(findings, checkAttribution(repoPath)...)
	} else {
		findings = append(findings, checkRegisteredRepos()...)
	}

	findings = append(findings, checkSync()...)

	render(w, findings)

	// Only genuine breakage fails. An unconfigured optional part and a
	// check that could not run are both reported without affecting this,
	// so `dun verify` can gate CI without failing a local-only setup.
	for _, f := range findings {
		if f.Level == levelBroken {
			return fmt.Errorf("%d problem(s) found", countBroken(findings))
		}
	}
	return nil
}

func countBroken(fs []finding) int {
	n := 0
	for _, f := range fs {
		if f.Level == levelBroken {
			n++
		}
	}
	return n
}

// verifyRepoPath resolves which repository to check.
func verifyRepoPath(repoFlag string) (string, bool) {
	if repoFlag != "" {
		if _, _, err := resolveRepo(repoFlag); err != nil {
			return "", false
		}
		return repoFlag, true
	}
	if inGitRepo("") {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		return cwd, true
	}
	return "", false
}

func checkInstall() []finding {
	var out []finding

	path, err := exec.LookPath("dun")
	if err != nil {
		// The hooks resolve `dun` from PATH at run time, so a binary that
		// is not on PATH means every hook silently does nothing.
		return append(out, finding{
			Area:  "install",
			Level: levelBroken,
			Detail: "dun is not on PATH — the git hooks resolve it from PATH, " +
				"so attribution is not running at all",
			Fix: "brew install navjyotnishant/tap/dun",
		})
	}

	out = append(out, finding{
		Area:   "install",
		Level:  levelOK,
		Detail: path,
	})
	return out
}

func checkAgents(repoFlag string) []finding {
	cwd := repoFlag
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil
		}
	}

	var out []finding
	var anyFound bool

	for _, d := range adapter.Detect(cwd) {
		switch d.State {
		case adapter.StateFound:
			anyFound = true
			out = append(out, finding{
				Area:   "agent: " + d.Agent,
				Level:  levelOK,
				Detail: fmt.Sprintf("%d session(s) for this repository", d.Sessions),
			})
		case adapter.StateEmpty:
			anyFound = true
			out = append(out, finding{
				Area:   "agent: " + d.Agent,
				Level:  levelInfo,
				Detail: "installed, but has not been used in this repository",
			})
		case adapter.StateMissing:
			// Configured and absent is a mistake; not installed is not.
			out = append(out, finding{
				Area:   "agent: " + d.Agent,
				Level:  levelBroken,
				Detail: "configured path does not exist: " + d.Root,
				Fix:    "dun config set agent." + d.Agent + ".path <dir>",
			})
		case adapter.StateError:
			out = append(out, finding{
				Area:   "agent: " + d.Agent,
				Level:  levelUnknown,
				Detail: "could not read: " + d.Err.Error(),
			})
		default:
			out = append(out, finding{
				Area:   "agent: " + d.Agent,
				Level:  levelInfo,
				Detail: "not installed",
			})
		}
	}

	if !anyFound {
		out = append(out, finding{
			Area:  "agents",
			Level: levelBroken,
			Detail: "no agent transcripts found anywhere — every commit will be " +
				"stamped undetermined",
			Fix: "dun config agents",
		})
	}
	return out
}

func checkHooks(repoPath string) []finding {
	gitDir, err := gitDirFor(repoPath)
	if err != nil {
		return []finding{{
			Area:   "hooks",
			Level:  levelUnknown,
			Detail: "could not locate .git: " + err.Error(),
		}}
	}

	var missing []string
	var notExecutable []string
	for _, name := range trackedHooks {
		path := filepath.Join(gitDir, "hooks", name)
		info, err := os.Stat(path)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		if info.Mode()&0o111 == 0 {
			// A hook git cannot execute is the same as no hook, and gives
			// no error when git skips it.
			notExecutable = append(notExecutable, name)
		}
	}

	var out []finding
	if len(missing) > 0 {
		out = append(out, finding{
			Area:   "hooks",
			Level:  levelBroken,
			Detail: "not installed: " + strings.Join(missing, ", "),
			Fix:    "dun init",
		})
	}
	if len(notExecutable) > 0 {
		out = append(out, finding{
			Area:   "hooks",
			Level:  levelBroken,
			Detail: "not executable: " + strings.Join(notExecutable, ", "),
			Fix:    "dun init",
		})
	}
	if len(out) == 0 {
		out = append(out, finding{
			Area:   "hooks",
			Level:  levelOK,
			Detail: strings.Join(trackedHooks, ", "),
		})
	}
	return out
}

func checkJournal(repoPath string) []finding {
	dataDir, err := journalDataDir()
	if err != nil {
		return []finding{{Area: "journal", Level: levelUnknown, Detail: err.Error()}}
	}

	repoID, err := repoIDFor(repoPath)
	if err != nil {
		return []finding{{
			Area:   "journal",
			Level:  levelUnknown,
			Detail: "could not identify this repository: " + err.Error(),
		}}
	}

	entries, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil {
		return []finding{{
			Area:   "journal",
			Level:  levelBroken,
			Detail: "unreadable: " + err.Error(),
			Fix:    "dun journal show",
		}}
	}
	if len(entries) == 0 {
		return []finding{{
			Area:  "journal",
			Level: levelInfo,
			Detail: "no events recorded for this repository yet — commit with an " +
				"agent-edited file and this fills in",
		}}
	}

	// Recency, not just presence. A journal that stopped growing is the
	// symptom shared by almost every silent failure this tool has: an
	// agent changed its format, a path broke, an upgrade went sideways.
	var last time.Time
	for _, e := range entries {
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	detail := fmt.Sprintf("%d events, last written %s", len(entries), humanAge(last))

	lvl := levelOK
	if time.Since(last) > 30*24*time.Hour {
		lvl = levelUnknown
		detail += " — nothing recorded in a month, which may mean collection has stopped"
	}
	return []finding{{Area: "journal", Level: lvl, Detail: detail}}
}

// checkAttribution runs the real determination and reports what it yields.
//
// The check that matters most: a setup can pass every individual test and
// still produce undetermined, and only running it end to end finds that.
//
// Reads only. determineTrailer ingests into the journal as a side effect,
// so it is deliberately not called here — verification must not change
// what it is measuring, and someone debugging will run this repeatedly.
func checkAttribution(repoPath string) []finding {
	cwd := repoPath
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil
		}
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	var events int
	for _, ad := range adapter.All() {
		paths, err := ad.SessionFiles(cwd)
		if err != nil {
			continue
		}
		for _, p := range paths {
			parsed, err := ad.ParseSince(p, since)
			if err != nil {
				continue
			}
			events += len(parsed)
		}
	}

	if events == 0 {
		return []finding{{
			Area:  "attribution",
			Level: levelInfo,
			Detail: "no agent activity in the last 7 days — commits will be stamped " +
				"undetermined until there is",
		}}
	}
	return []finding{{
		Area:   "attribution",
		Level:  levelOK,
		Detail: fmt.Sprintf("%d agent edits in the last 7 days are available to attribute", events),
	}}
}

// checkRegisteredRepos checks every instrumented repository, not just that
// they are registered.
//
// "3 instrumented" says nothing about whether any of them works. A
// repository with a missing hook or a journal that stopped growing would
// pass a count, and those are exactly the silent failures this command
// exists to surface — in the one view that reaches a repository nobody has
// visited in months.
func checkRegisteredRepos() []finding {
	entries, err := registry.List()
	if err != nil {
		return []finding{{Area: "repositories", Level: levelUnknown, Detail: err.Error()}}
	}
	if len(entries) == 0 {
		return []finding{{
			Area:   "repositories",
			Level:  levelInfo,
			Detail: "none instrumented yet",
			Fix:    "dun init",
		}}
	}

	var out []finding
	for _, e := range entries {
		name := shortRepoName(e.Path)

		// A repository can move after init recorded it. Its journal rows
		// outlive the working tree, so this is reported rather than
		// dropped — but it is a fact about the machine, not a fault.
		if !inGitRepo(e.Path) {
			out = append(out, finding{
				Area:   name,
				Level:  levelInfo,
				Detail: "moved or deleted — " + e.Path,
			})
			continue
		}

		var problems []string
		for _, f := range checkHooks(e.Path) {
			if f.Level == levelBroken {
				problems = append(problems, "hooks "+f.Detail)
			}
		}

		if len(problems) > 0 {
			out = append(out, finding{
				Area:   name,
				Level:  levelBroken,
				Detail: strings.Join(problems, "; "),
				Fix:    "dun init --repo " + e.Path,
			})
			continue
		}
		out = append(out, finding{Area: name, Level: levelOK, Detail: journalSummary(e.Path)})
	}
	return out
}

// journalSummary describes what a repository has recorded, leading with
// recency: a journal that stopped growing is the shared symptom of nearly
// every silent failure this tool has.
func journalSummary(repoPath string) string {
	dataDir, err := journalDataDir()
	if err != nil {
		return "journal unreadable"
	}
	repoID, _, err := resolveRepo(repoPath)
	if err != nil {
		return "could not identify this repository"
	}
	entries, err := journal.ReadRange(dataDir, repoID, time.Time{}, time.Time{})
	if err != nil {
		return "journal unreadable"
	}
	if len(entries) == 0 {
		return "hooks installed, nothing recorded yet"
	}
	var last time.Time
	for _, e := range entries {
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	return fmt.Sprintf("%d events, last %s", len(entries), humanAge(last))
}

func checkSync() []finding {
	cfg, err := config.Load()
	if err != nil {
		return []finding{{Area: "sync", Level: levelUnknown, Detail: err.Error()}}
	}
	if !cfg.Sync.Configured() {
		// Not a failure. Local-only is a supported way to use this tool,
		// offered by the setup wizard itself.
		return []finding{{
			Area:   "sync",
			Level:  levelInfo,
			Detail: "not configured — attribution stays on this machine",
			Fix:    "dun config datalake",
		}}
	}

	dsn, err := cfg.Sync.Resolve()
	if err != nil {
		return []finding{{
			Area:   "sync",
			Level:  levelBroken,
			Detail: err.Error(),
			Fix:    "dun config datalake",
		}}
	}

	db, err := sidecar.Open(dsn)
	if err != nil {
		return []finding{{
			Area:   "sync",
			Level:  levelUnknown,
			Detail: "could not open " + cfg.Sync.Redacted() + ": " + err.Error(),
		}}
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		// Unreachable is "could not check", not "broken": the database
		// may simply be down right now, and the push hook already handles
		// that without losing anything.
		return []finding{{
			Area:   "sync",
			Level:  levelUnknown,
			Detail: "unreachable: " + cfg.Sync.Redacted(),
		}}
	}
	return []finding{{
		Area:   "sync",
		Level:  levelOK,
		Detail: cfg.Sync.Redacted(),
	}}
}

func render(w io.Writer, fs []finding) {
	c := termcolor.New(w)

	for _, f := range fs {
		var mark string
		switch f.Level {
		case levelOK:
			mark = c.S(termcolor.Good, "ok")
		case levelInfo:
			mark = c.S(termcolor.Muted, "--")
		case levelUnknown:
			mark = c.S(termcolor.Warn, "??")
		case levelBroken:
			mark = c.S(termcolor.Warn, "!!")
		}
		fmt.Fprintf(w, "  %s  %-30s %s\n", mark, f.Area, f.Detail)
	}

	// Fixes are collected at the end rather than inline, so someone with
	// three problems gets three commands together rather than hunting for
	// them among the passing checks.
	var fixes []finding
	for _, f := range fs {
		if f.Fix != "" && (f.Level == levelBroken || f.Level == levelInfo) {
			fixes = append(fixes, f)
		}
	}
	if len(fixes) > 0 {
		fmt.Fprintln(w)
		for _, f := range fixes {
			fmt.Fprintf(w, "  %s  %s\n", c.S(termcolor.Muted, f.Area+":"), c.S(termcolor.Bold, f.Fix))
		}
	}

	fmt.Fprintln(w)
	if n := countBroken(fs); n > 0 {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Warn, fmt.Sprintf("%d problem(s) need attention", n)))
	} else {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Good, "attribution is working"))
	}
}

// humanAge renders how long ago something happened, in the coarsest unit
// that is still informative.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "less than an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hour(s) ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d day(s) ago", int(d.Hours()/24))
	default:
		return t.Format("2 Jan 2006")
	}
}

// repoIDFor resolves a repository's id, defaulting to the current one.
func repoIDFor(repoPath string) (string, error) {
	if repoPath == "" {
		return currentRepoID()
	}
	id, _, err := resolveRepo(repoPath)
	return id, err
}
