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
	root.AddCommand(newReposUpdateCmd())
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

// decodeSlug reverses Claude Code's directory encoding, which replaces every
// non-alphanumeric character with a dash.
//
// The encoding is heavily lossy and deliberately so — see
// claudecode.SlugForCwd. A dash in a slug was a '/', a '.', a '_', a space,
// or a literal '-', and nothing in the name says which. Rather than guess,
// this walks candidates and returns the first that exists on disk.
//
// Two independent ambiguities, resolved in this order:
//
//  1. how many trailing dashes belong to the final segment rather than
//     separating directories, and
//  2. for each remaining dash, whether it was a separator or a character
//     inside a name.
//
// (2) is why this is not simply a strings.Split: after SlugForCwd started
// replacing the full character class, a path like ~/.orion or /tmp/my_repo
// encodes its dot and underscore as dashes too, so splitting on every dash
// produces segments that never existed. That regressed discovery to zero
// results on any path containing punctuation.
//
// Bounded on purpose. A slug with many dashes has exponentially many
// readings and this is a convenience command, not the attribution path — so
// the character-level search runs only over a limited number of dashes and
// otherwise falls back to the separator-only reading. Missing a candidate
// costs a row in `dun repos candidates`; it never affects what a commit is
// stamped with.
func decodeSlug(slug string) string {
	// The prefix says where the path started, and the two platforms differ.
	//
	// A Unix slug begins with the dash that encoded the leading '/', so the
	// root has to be put back. A Windows slug begins with the drive letter,
	// whose colon is now also encoded as a dash — "C--Users-me-repo" has to
	// become "C:\Users\me\repo".
	var root string
	var rest string
	switch {
	case strings.HasPrefix(slug, "-"):
		root = string(filepath.Separator)
		rest = strings.TrimPrefix(slug, "-")
	case len(slug) > 1 && slug[1] == '-' && isDriveLetter(slug[0]):
		root = string(slug[0]) + `:\`
		// Two dashes where the colon and the separator both encoded.
		rest = strings.TrimPrefix(slug[2:], "-")
	default:
		return ""
	}

	parts := strings.Split(rest, "-")

	// Try the longest prefix as directory names first, then progressively
	// treat trailing dashes as literal characters in the final segment.
	for join := 0; join < len(parts); join++ {
		segments := append([]string{}, parts[:len(parts)-join]...)
		if join > 0 {
			segments[len(segments)-1] = strings.Join(parts[len(parts)-join-1:], "-")
		}
		candidate := root + filepath.Join(segments...)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	// Nothing read as a pure separator split. Some dash was a character
	// inside a name — a dot, an underscore, a space. Search those readings.
	if p := decodeSlugWithLiterals(root, parts); p != "" {
		return p
	}
	return ""
}

// maxDashSearch bounds the character-level search. Each undecided dash
// multiplies the candidate count, so this is a deliberate ceiling rather
// than a tuned number: enough for a dotted parent plus a couple of
// underscores, which is what real paths look like.
const maxDashSearch = 12

// decodeSlugWithLiterals tries readings where some dashes were characters
// inside a directory name rather than separators.
//
// Walks left to right, extending the current segment or closing it, and
// stops at the first reading that exists on disk. Depth-bounded by
// maxDashSearch; a slug with more dashes than that falls back to the
// separator-only reading its caller already tried.
func decodeSlugWithLiterals(root string, parts []string) string {
	if len(parts) < 2 || len(parts) > maxDashSearch {
		return ""
	}

	// extend returns a NEW slice every time. Doing this with
	// append(append([]string{}, segs...), x) looks equivalent and is not:
	// the inner append can return a slice with spare capacity, so two
	// sibling branches append into the same backing array and the second
	// silently overwrites the first's last segment. That produced a search
	// which explored a mutated tree and found nothing.
	extend := func(segs []string, s string) []string {
		out := make([]string, len(segs)+1)
		copy(out, segs)
		out[len(segs)] = s
		return out
	}

	// replaceLast returns a NEW slice with the final segment rewritten,
	// for the reading where this dash was a character inside a name.
	replaceLast := func(segs []string, s string) []string {
		out := make([]string, len(segs))
		copy(out, segs)
		out[len(segs)-1] = s
		return out
	}

	// Every replaced character reads back identically, so a literal tried
	// here stands for whichever one it was. Ordered by what actually shows
	// up in paths: dots (dotted parents), underscores, then the rest.
	literals := []string{".", "_", "-", " "}

	var walk func(segments []string, i int) string
	walk = func(segments []string, i int) string {
		if i == len(parts) {
			candidate := root + filepath.Join(segments...)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
			return ""
		}

		// Prune on the parent only. The last segment is still being built —
		// a dash later in the slug may extend it — so requiring it to exist
		// now would reject every path whose name contains a replaced
		// character, which is the entire case this function exists for.
		if len(segments) > 1 {
			prefix := root + filepath.Join(segments[:len(segments)-1]...)
			if info, err := os.Stat(prefix); err != nil || !info.IsDir() {
				return ""
			}
		}

		// An empty part means two dashes ran together: a separator
		// immediately followed by a replaced character, as in "qp--3xbs"
		// for "qp/_3xbs". It is never a segment of its own — it says the
		// NEXT segment opens with a literal.
		if parts[i] == "" {
			if i+1 >= len(parts) {
				return ""
			}
			for _, lit := range literals {
				if p := walk(extend(segments, lit+parts[i+1]), i+2); p != "" {
					return p
				}
			}
			return ""
		}

		// This dash was a separator: start a new segment.
		if p := walk(extend(segments, parts[i]), i+1); p != "" {
			return p
		}

		// This dash was a character inside the previous segment.
		if len(segments) > 0 {
			last := segments[len(segments)-1]
			for _, lit := range literals {
				if p := walk(replaceLast(segments, last+lit+parts[i]), i+1); p != "" {
					return p
				}
			}
		}
		return ""
	}

	return walk(nil, 0)
}

func isDriveLetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
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
