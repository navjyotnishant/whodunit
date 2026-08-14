// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: `dun log` — what the hooks did, and what they failed to do.

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	var (
		repoFlag string
		limit    int
		follow   bool
		errsOnly bool
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show what the git hooks did, including errors they survived.",
		Long: "The hooks are deliberately quiet: a commit must never fail because\n" +
			"attribution failed, and the push hook prints only when a sync\n" +
			"breaks. That silence is right, and it leaves nothing to look at\n" +
			"when something is wrong.\n\n" +
			"This is that record — what ran, what it decided, and every error\n" +
			"the hooks swallowed rather than blocking your work with.\n\n" +
			"It holds no prompt text and no file contents. Paths and counts\n" +
			"only.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLog(cmd.OutOrStdout(), repoFlag, limit, follow, errsOnly)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "only this repository (path or id)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "how many entries to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep watching for new entries")
	cmd.Flags().BoolVar(&errsOnly, "errors", false, "only warnings and panics")
	return cmd
}

func runLog(w io.Writer, repoFlag string, limit int, follow, errsOnly bool) error {
	home, err := config.Dir()
	if err != nil {
		return err
	}

	// An explicit --repo is resolved through the same resolver every other
	// command uses, so a path, an id, or a repository that has since moved
	// all behave the way they do in `dun status` and `dun journal`.
	//
	// Without the flag the log is not scoped to the current directory: the
	// most common reason to run this is that something is wrong, and
	// narrowing to wherever the reader happens to be standing hides the
	// entries from the repository that broke.
	var repoID string
	if repoFlag != "" {
		id, _, err := resolveRepo(repoFlag)
		if err != nil {
			return err
		}
		repoID = id
	}

	entries, err := hooklog.Read(home, 0)
	if err != nil {
		return err
	}
	entries = filterLog(entries, repoID, errsOnly)

	if len(entries) == 0 && !follow {
		printEmptyLog(w, repoFlag != "" || errsOnly)
		return nil
	}

	// Oldest first for display: reversed on read so the newest survive the
	// limit, then flipped back so the reader scrolls forward through time
	// and ends at the most recent line — which is where --follow continues.
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	for _, e := range entries {
		printLogEntry(w, e)
	}

	if follow {
		return followLog(w, home, repoID, errsOnly)
	}
	return nil
}

// filterLog narrows to one repository and, optionally, to problems.
func filterLog(entries []hooklog.Entry, repoID string, errsOnly bool) []hooklog.Entry {
	var out []hooklog.Entry
	for _, e := range entries {
		if repoID != "" && e.RepoID != repoID {
			continue
		}
		if errsOnly && e.Level == hooklog.LevelInfo {
			continue
		}
		out = append(out, e)
	}
	return out
}

// followLog polls for new entries and prints them as they arrive.
//
// Polling rather than watching the filesystem: the log is appended to by a
// separate process a few times per commit, so a one-second poll is
// indistinguishable from instant to a human watching, and it needs no
// platform-specific watcher for a command that mostly runs for a minute at
// a time.
func followLog(w io.Writer, home, repoID string, errsOnly bool) error {
	last := time.Now()
	for {
		time.Sleep(time.Second)

		entries, err := hooklog.Read(home, 0)
		if err != nil {
			continue // a rotation mid-read is not worth stopping for
		}

		// Reverse to oldest-first, then take what is newer than the last
		// thing printed. Comparing timestamps rather than counting lines
		// survives rotation, which drops old entries and would otherwise
		// make the count go backwards.
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
		for _, e := range filterLog(entries, repoID, errsOnly) {
			if e.Time.After(last) {
				printLogEntry(w, e)
				last = e.Time
			}
		}
	}
}

func printLogEntry(w io.Writer, e hooklog.Entry) {
	c := termcolor.New(w)

	style := termcolor.Muted
	switch e.Level {
	case hooklog.LevelWarn:
		style = termcolor.Warn
	case hooklog.LevelPanic:
		style = termcolor.Bad
	}

	fmt.Fprintf(w, "%s  %s  %s",
		c.S(termcolor.Muted, e.Time.Format("01-02 15:04:05")),
		c.S(style, fmt.Sprintf("%-5s", e.Level)),
		c.S(termcolor.Bold, e.Hook))

	if e.Repo != "" {
		fmt.Fprintf(w, " %s", c.S(termcolor.Muted, shortRepoName(e.Repo)))
	}
	fmt.Fprintf(w, "  %s", e.Detail)
	fmt.Fprintln(w)

	// The stack is the whole value of a panic entry, and it is the one
	// thing nobody can reproduce on demand.
	if e.Stack != "" {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, indent(e.Stack, "      ")))
	}
}

// printEmptyLog says why there is nothing rather than printing nothing.
//
// An empty log and a broken one look identical otherwise, which is the
// exact confusion this command exists to remove.
func printEmptyLog(w io.Writer, filtered bool) {
	c := termcolor.New(w)

	if filtered {
		fmt.Fprintln(w, "no entries match.")
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
			"drop --repo or --errors to see everything recorded"))
		return
	}

	fmt.Fprintln(w, "nothing recorded yet.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
		"entries appear when a hook runs — commit in an instrumented"))
	fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
		"repository, or push one with sync configured."))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "check the hooks are installed:  %s\n", c.S(termcolor.Bold, "dun verify"))
}

// indent prefixes every line, for a stack trace under its entry.
func indent(s, prefix string) string {
	var out []byte
	for _, line := range splitLines(s) {
		if line == "" {
			continue
		}
		out = append(out, prefix...)
		out = append(out, line...)
		out = append(out, '\n')
	}
	return string(trimTrailingNewline(out))
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func trimTrailingNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

// logPath is where the log lives, for the odd case of wanting to tail it
// with something other than this command.
func logPath() string {
	home, err := config.Dir()
	if err != nil {
		return ""
	}
	return hooklog.Dir(home) + string(os.PathSeparator) + "hooks.log"
}
