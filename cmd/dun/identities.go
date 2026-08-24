// Author: Navjyot Nishant
// Created: 2026-08-24
// Last updated: 2026-08-24
// Description: Reporting the git identities that authored instrumented
// repositories, and suggesting which of them are the same person.

package main

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

func newIdentitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "identities",
		Short: "Show the git identities in your instrumented repositories.",
		Long: "Lists every git committer email found in the repositories you have\n" +
			"instrumented, and flags addresses that look like the same person.\n\n" +
			"One machine configured with a GitHub noreply address and another with\n" +
			"a real one gives you two identities without ever saying so. Both are\n" +
			"valid, both commit, and every per-person figure is then split between\n" +
			"them.\n\n" +
			"Suggestions are printed, never applied. Merging two identities is a\n" +
			"claim about who somebody is, and this command does not make it for\n" +
			"you - it prints the configuration and leaves the decision where it\n" +
			"belongs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runIdentities(cmd)
		},
	}
}

// identity is one git committer email and the names it commits under.
type identity struct {
	Email   string
	Names   map[string]bool
	Commits int
}

func runIdentities(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	c := termcolor.New(w)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repos, err := registry.List()
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Fprintln(w, "No instrumented repositories. Run `dun init` in one first.")
		return nil
	}

	found := map[string]*identity{}
	for _, r := range repos {
		collectIdentities(r.Path, found)
	}
	if len(found) == 0 {
		fmt.Fprintln(w, "No commits found in the instrumented repositories.")
		return nil
	}

	emails := make([]string, 0, len(found))
	for e := range found {
		emails = append(emails, e)
	}
	sort.Slice(emails, func(i, j int) bool {
		return found[emails[i]].Commits > found[emails[j]].Commits
	})

	fmt.Fprintf(w, "identities across %d instrumented repositor%s:\n\n",
		len(repos), plural(int64(len(repos))))
	for _, e := range emails {
		id := found[e]
		line := fmt.Sprintf("  %-44s %5d commit(s)", e, id.Commits)
		if canonical := cfg.ResolveIdentity(e); canonical != e {
			line += c.S(termcolor.Muted, "  -> "+canonical)
		}
		fmt.Fprintln(w, line)
	}

	printIdentitySuggestions(w, c, cfg, found, emails)
	return nil
}

// printIdentitySuggestions reports addresses that appear to be one person
// and prints the configuration that would say so.
func printIdentitySuggestions(w io.Writer, c *termcolor.Writer,
	cfg config.Config, found map[string]*identity, emails []string) {
	// Two signals, because neither alone finds the real cases without
	// also inventing false ones.
	//
	// The display name is evidence the person wrote themselves, but it is
	// not always a person: a tool that commits on your behalf writes its
	// own name, and grouping on that merges every address that ever used
	// the tool. Bracketed names are excluded for exactly that reason.
	//
	// The address's local part catches what the name misses. A GitHub
	// noreply address is `NNNNN+handle@users.noreply.github.com`, and the
	// handle is usually the same string the person uses elsewhere - which
	// is how one human ends up split across two addresses that share no
	// display name at all.
	byKey := map[string][]string{}
	add := func(key, email string) {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return
		}
		for _, seen := range byKey[key] {
			if seen == email {
				return
			}
		}
		byKey[key] = append(byKey[key], email)
	}
	for _, e := range emails {
		add(identityHandle(e), e)
		for n := range found[e].Names {
			if isToolName(n) {
				continue
			}
			add(n, e)
		}
	}
	byName := byKey

	type suggestion struct {
		Name      string
		Canonical string
		Aliases   []string
	}
	var out []suggestion
	for name, group := range byName {
		if len(group) < 2 || name == "" {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return found[group[i]].Commits > found[group[j]].Commits
		})
		// Already configured is not a suggestion.
		allResolved := true
		for _, e := range group[1:] {
			if cfg.ResolveIdentity(e) != cfg.ResolveIdentity(group[0]) {
				allResolved = false
			}
		}
		if allResolved {
			continue
		}
		out = append(out, suggestion{Name: name, Canonical: group[0], Aliases: group[1:]})
	}
	if len(out) == 0 {
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
		"these addresses look like one person - same name, or the same handle:"))
	for _, s := range out {
		fmt.Fprintf(w, "\n  %s\n", s.Name)
		fmt.Fprintf(w, "    keep    %s\n", s.Canonical)
		for _, a := range s.Aliases {
			fmt.Fprintf(w, "    alias   %s\n", a)
		}
	}
	fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
		"nothing has been changed. to apply, add to ~/.whodunit/config.json:"))
	fmt.Fprintln(w, "\n  \"identities\": {")
	var lines []string
	for _, s := range out {
		for _, a := range s.Aliases {
			lines = append(lines, fmt.Sprintf("    %q: %q", a, s.Canonical))
		}
	}
	fmt.Fprintln(w, strings.Join(lines, ",\n"))
	fmt.Fprintln(w, "  }")
}

// collectIdentities reads committer emails and names from one repository.
// A repository that has moved or been deleted contributes nothing rather
// than failing the command: the registry records where a repo was, not
// where it is.
func collectIdentities(dir string, into map[string]*identity) {
	cmd := exec.Command("git", "log", "--format=%ae%x01%an")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		email, name, ok := strings.Cut(strings.TrimSpace(line), "\x01")
		if !ok || email == "" {
			continue
		}
		id := into[email]
		if id == nil {
			id = &identity{Email: email, Names: map[string]bool{}}
			into[email] = id
		}
		id.Commits++
		if name != "" {
			id.Names[name] = true
		}
	}
}

// identityHandle is the part of an address that identifies a person rather
// than a mailbox: the local part, with a GitHub noreply address's numeric
// prefix removed so `14622560+alice@...` and `alice@...` agree.
func identityHandle(email string) string {
	local, _, ok := strings.Cut(email, "@")
	if !ok {
		return ""
	}
	if _, handle, ok := strings.Cut(local, "+"); ok && handle != "" {
		return handle
	}
	return local
}

// isToolName reports whether a git author name reads as a tool rather than
// a person. Bots and generators conventionally bracket their names, and
// grouping on one merges every address that ever ran it.
func isToolName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasPrefix(name, "[") || strings.HasSuffix(name, "]")
}
