package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show trailer coverage and method mix.",
		Long: "Reports coverage and method mix for a repository's recent commits.\n\n" +
			"Inside a repository, reports that one. Outside any repository, lists\n" +
			"every repository you have instrumented, so a machine-wide view needs\n" +
			"no visiting each one in turn.\n\n" +
			"--repo reports a specific repository from anywhere; it takes a path\n" +
			"or a repo id, the same as `dun journal --repo`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, repoFlag)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path or repo id to report (default: current directory)")
	return cmd
}

func runStatus(cmd *cobra.Command, repoFlag string) error {
	w := cmd.OutOrStdout()

	if repoFlag != "" {
		// Resolve for the error messages, which name what is wrong with a
		// path rather than letting git fail obscurely later.
		_, label, err := resolveRepo(repoFlag)
		if err != nil {
			return err
		}
		dir := repoFlag
		if !isRepoID(repoFlag) {
			return statusFor(w, dir, label)
		}
		// A repo id has no working tree to run git in; the registry knows
		// where it was last seen.
		path, ok := pathForRepoID(repoFlag)
		if !ok {
			return fmt.Errorf("repo id %s is not in the registry, so there is no "+
				"working tree to read git history from", repoFlag[:min(12, len(repoFlag))])
		}
		return statusFor(w, path, path)
	}

	// Inside a repository: report it, as before.
	if inGitRepo("") {
		return statusFor(w, "", "")
	}

	// Outside any repository, listing every instrumented one beats failing
	// with git's own error, which says nothing about what to do.
	return statusAcrossRepos(w)
}

// statusFor reports one repository. An empty dir means the current one.
func statusFor(w io.Writer, dir, label string) error {
	s, err := scanRepo(dir)
	if err != nil {
		return err
	}

	if label != "" {
		fmt.Fprintf(w, "%s\n", label)
	}
	fmt.Fprintf(w, "commits examined:  %d\n", s.Total)
	if s.Total == 0 {
		return nil
	}

	c := termcolor.New(w)
	fmt.Fprintf(w, "coverage:          %d/%d (%.0f%%)\n", s.Covered, s.Total, s.CoveragePct())
	fmt.Fprintln(w, "method mix:")
	for _, m := range methodDisplayOrder {
		n := s.MethodCount[m]
		if n == 0 {
			continue
		}
		// Pad before styling: the escape sequences are zero-width on
		// screen but not to %-13s, so styling first breaks alignment.
		label := fmt.Sprintf("%-13s", m)
		// Each line explains itself. "intersected 21" means nothing to
		// someone meeting the vocabulary for the first time, and the
		// person reading their own coverage is exactly who needs to know
		// what it claims.
		fmt.Fprintf(w, "  %s %4d   %s\n",
			c.S(termcolor.MethodStyle(string(m)), label), n,
			c.S(termcolor.Muted, m.Explain()))
	}
	return nil
}

// statusAcrossRepos summarises every instrumented repository, one line each.
func statusAcrossRepos(w io.Writer) error {
	entries, err := registry.List()
	if err != nil {
		return err
	}

	c := termcolor.New(w)
	if len(entries) == 0 {
		// An empty list with no explanation reads like a bug rather than a
		// state, so it says what to do next.
		fmt.Fprintln(w, "no repositories are instrumented yet.")
		fmt.Fprintf(w, "\ninstrument one with:  %s\n", c.S(termcolor.Bold, "dun init"))
		return nil
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	fmt.Fprintf(w, "%d instrumented repositor%s\n\n", len(entries), plural2(len(entries)))

	var available int
	for _, e := range entries {
		name := shortRepoName(e.Path)

		// A repository can move or be deleted after `dun init` recorded it.
		// Its journal rows survive, so the row is shown as unavailable
		// rather than dropped — the recorded history is still real, and
		// silently omitting it would look like it was never instrumented.
		if !inGitRepo(e.Path) {
			fmt.Fprintf(w, "  %-28s %s\n", name,
				c.S(termcolor.Muted, "moved or deleted — "+e.Path))
			continue
		}
		available++

		s, err := scanRepo(e.Path)
		if err != nil {
			fmt.Fprintf(w, "  %-28s %s\n", name, c.S(termcolor.Warn, "unreadable"))
			continue
		}
		if s.Total == 0 {
			fmt.Fprintf(w, "  %-28s %s\n", name, c.S(termcolor.Muted, "no commits yet"))
			continue
		}

		fmt.Fprintf(w, "  %-28s %3.0f%% coverage  %s\n",
			name, s.CoveragePct(), c.S(termcolor.Muted, methodSummary(s)))
	}

	if available > 0 {
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
			"one repository in detail:  dun status --repo <path>"))
	}
	return nil
}

// coverageStats is one repository's trailer coverage.
type coverageStats struct {
	Total       int
	Covered     int
	MethodCount map[spec.Method]int
}

func (s coverageStats) CoveragePct() float64 {
	if s.Total == 0 {
		return 0
	}
	return 100 * float64(s.Covered) / float64(s.Total)
}

var methodDisplayOrder = []spec.Method{
	spec.MethodIntersected, spec.MethodObserved, spec.MethodInferred,
	spec.MethodDeclared, spec.MethodUndetermined,
}

// methodSummary renders the mix compactly, strongest evidence first, for a
// one-line-per-repository listing.
func methodSummary(s coverageStats) string {
	var parts []string
	for _, m := range methodDisplayOrder {
		if n := s.MethodCount[m]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", m, n))
		}
	}
	if len(parts) == 0 {
		return "no trailers"
	}
	return strings.Join(parts, ", ")
}

// scanRepo reads trailer coverage from a repository's recent commits. An
// empty dir means the current working directory.
func scanRepo(dir string) (coverageStats, error) {
	s := coverageStats{MethodCount: map[spec.Method]int{}}

	cmd := exec.Command("git", "log", "-n", "100", "--format=%B%x00")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// An unborn repository is a valid zero-commit status, not a
		// failure — anything else genuinely is.
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return s, nil
		}
		return s, fmt.Errorf("read git log: %w", err)
	}

	prefix := spec.TrailerKey + ":"
	for _, commitMsg := range strings.Split(string(out), "\x00") {
		commitMsg = strings.TrimSpace(commitMsg)
		if commitMsg == "" {
			continue
		}
		s.Total++

		scanner := bufio.NewScanner(strings.NewReader(commitMsg))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			t, err := spec.Parse(strings.TrimSpace(line[len(prefix):]))
			if err != nil {
				continue
			}
			s.Covered++
			s.MethodCount[t.Method]++
			break
		}
	}
	return s, nil
}

// inGitRepo reports whether dir is inside a git working tree. An empty dir
// means the current directory.
func inGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// isRepoID reports whether s looks like a repo id rather than a path.
func isRepoID(s string) bool { return looksLikeRepoID(s) }

// pathForRepoID finds where a registered repository lives.
func pathForRepoID(repoID string) (string, bool) {
	entries, err := registry.List()
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.RepoID == repoID {
			return e.Path, true
		}
	}
	return "", false
}

// shortRepoName is the last two path segments, which identifies a
// repository without a column of identical parent directories.
func shortRepoName(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, string(os.PathSeparator)), string(os.PathSeparator))
	if len(parts) <= 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}
