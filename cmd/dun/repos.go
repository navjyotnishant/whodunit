package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
	"github.com/spf13/cobra"
)

func newReposCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "repos",
		Short: "List repositories instrumented with dun.",
	}
	root.AddCommand(newReposListCmd())
	root.AddCommand(newReposCandidatesCmd())
	root.AddCommand(newReposRemoveCmd())
	return root
}

func newReposListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show every repository that has had `dun init` run in it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := registry.List()
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if len(entries) == 0 {
				fmt.Fprintln(w, "no repositories instrumented yet — run `dun init` in one")
				return nil
			}

			for _, e := range entries {
				marker := ""
				if _, err := os.Stat(e.Path); os.IsNotExist(err) {
					// The repo moved or was deleted. Say so rather than
					// printing a path that no longer exists as if it were live.
					marker = "  (path no longer exists)"
				}
				fmt.Fprintf(w, "%s  %s%s\n", e.RepoID[:8], e.Path, marker)
			}
			return nil
		},
	}
}

func newReposCandidatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "candidates",
		Short: "Show repositories with agent transcripts that are NOT instrumented.",
		Long: "Lists repositories where Claude Code has been used but `dun init` has\n" +
			"not been run.\n\n" +
			"This only reports. Instrumenting a repository stamps attribution\n" +
			"trailers into its commits, which is a disclosure decision — so it\n" +
			"stays an explicit `dun init` per repository rather than something\n" +
			"this command can do in bulk.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReposCandidates(cmd)
		},
	}
}

func runReposCandidates(cmd *cobra.Command) error {
	entries, err := registry.List()
	if err != nil {
		return err
	}
	instrumented := map[string]bool{}
	for _, e := range entries {
		instrumented[e.RepoID] = true
	}

	paths, err := candidatePaths()
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	// An agent run from a subdirectory produces its own transcript
	// directory, but it is the same repository — report each repo once, at
	// the shallowest path seen, rather than listing what looks like
	// several distinct candidates.
	seen := map[string]bool{}
	shown := 0
	for _, p := range paths {
		id, err := repoid.ForRepo(p)
		if err != nil {
			continue // not a git repo, or no commits yet — nothing to instrument
		}
		if instrumented[id] || seen[id] {
			continue
		}
		seen[id] = true
		fmt.Fprintf(w, "%s  %s\n", id[:8], p)
		shown++
	}

	if shown == 0 {
		fmt.Fprintln(w, "no uninstrumented repositories with agent transcripts found")
		return nil
	}
	fmt.Fprintf(w, "\n%d repositor%s with agent activity but no hooks.\n", shown, plural2(shown))
	fmt.Fprintln(w, "Instrument one with:  dun init --repo <path>")
	return nil
}

// candidatePaths derives repository paths from Claude Code's transcript
// directory names, which encode the working directory an agent ran in.
//
// This is discovery only. The set is large — every project an agent has
// ever been opened in — which is exactly why nothing here enrols anything.
//
// Claude Code only, deliberately. Discovery runs backwards from every other
// operation: instead of "where does this agent keep sessions for this
// repository", it asks "which repositories has this agent seen", which
// needs a reversible path encoding. Claude Code has one; Codex records the
// cwd inside each transcript, and Antigravity's CLI keys by workspace URI
// in a SQLite payload. Neither is enumerable without reading every file.
//
// Generalising would mean a `Discover() ([]string, error)` method most
// adapters implement as an expensive scan or a stub. Not worth it until a
// second agent can actually answer it (NAV-45).
func candidatePaths() ([]string, error) {
	root := claudecode.ProjectsDir()
	if root == "" {
		return nil, nil
	}
	dirs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent transcript directory: %w", err)
	}

	var paths []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := decodeSlug(d.Name())
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			continue // the project directory is gone
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// decodeSlug reverses Claude Code's directory encoding, which replaces each
// path separator with a dash.
//
// The encoding is lossy: a directory whose own name contains a dash is
// indistinguishable from a path separator. Rather than guess, this walks
// the candidates and returns the first that actually exists on disk.
func decodeSlug(slug string) string {
	if !strings.HasPrefix(slug, "-") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(slug, "-"), "-")

	// Try the longest prefix as directory names first, then progressively
	// treat trailing dashes as literal characters in the final segment.
	for join := 0; join < len(parts); join++ {
		segments := append([]string{}, parts[:len(parts)-join]...)
		if join > 0 {
			segments[len(segments)-1] = strings.Join(parts[len(parts)-join-1:], "-")
		}
		candidate := string(filepath.Separator) + filepath.Join(segments...)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func newReposRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Stop tracking the current repository for cross-repo tooling.",
		Long: "Removes the current repository from the registry.\n\n" +
			"This does not uninstall its hooks and does not delete its journal\n" +
			"entries — deregistering is not the same as forgetting. Use\n" +
			"`dun journal purge` to delete recorded observations.",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoID, err := currentRepoID()
			if err != nil {
				return err
			}
			removed, err := registry.Remove(repoID)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if !removed {
				fmt.Fprintln(w, "this repository was not in the registry")
				return nil
			}
			fmt.Fprintln(w, "removed from the registry")
			fmt.Fprintln(w, "hooks are still installed, and journal entries are untouched")
			return nil
		},
	}
}

func plural2(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
