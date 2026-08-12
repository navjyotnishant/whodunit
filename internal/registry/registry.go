// Package registry records which repositories have been instrumented with
// `dun init`.
//
// It exists so that anything operating across repositories — a multi-repo
// daemon, a cross-repo report — has an explicit, user-created list to work
// from. The alternative would be to discover repositories from Claude Code's
// transcript directory, which holds every project the user has ever opened
// an agent in: client work, throwaway experiments, other people's clones.
// Enrolling those silently would instrument repositories nobody chose, and
// start stamping AI-attribution trailers into commits where the user may
// not want to disclose agent use at all.
//
// Running `dun init` IS the opt-in. Nothing else adds an entry.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
)

// Entry is one instrumented repository.
type Entry struct {
	// RepoID is the repository's root commit SHA — the same identifier the
	// journal scopes rows by.
	RepoID string `json:"repo_id"`

	// Path is where the repository was when it was instrumented. It is a
	// convenience for humans and for a daemon deciding what to watch, not
	// an identity: a repo that moves keeps its RepoID and gets a corrected
	// Path on the next init.
	Path string `json:"path"`

	InstrumentedAt time.Time `json:"instrumented_at"`
}

type file struct {
	Repos []Entry `json:"repos"`
}

func path() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repos.json"), nil
}

// List returns every instrumented repository, ordered by path for stable
// output. An absent registry is an empty list, not an error.
func List() ([]Entry, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}

	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse registry at %s: %w", p, err)
	}
	sort.Slice(f.Repos, func(i, j int) bool { return f.Repos[i].Path < f.Repos[j].Path })
	return f.Repos, nil
}

// Add records a repository as instrumented. Re-running init on the same
// repository updates its path rather than adding a duplicate — a repo that
// moved is still the same repo.
func Add(repoID, repoPath string, now time.Time) error {
	if repoID == "" {
		return fmt.Errorf("registry: repo id is required")
	}

	entries, err := List()
	if err != nil {
		return err
	}

	updated := false
	for i := range entries {
		if entries[i].RepoID == repoID {
			entries[i].Path = repoPath
			updated = true
			break
		}
	}
	if !updated {
		entries = append(entries, Entry{
			RepoID:         repoID,
			Path:           repoPath,
			InstrumentedAt: now.UTC(),
		})
	}

	return write(entries)
}

// Remove drops a repository from the registry. It does not touch the
// repository's hooks or its journal entries — deregistering is not the same
// as forgetting, and conflating them would make `dun repos remove` silently
// destructive.
func Remove(repoID string) (bool, error) {
	entries, err := List()
	if err != nil {
		return false, err
	}

	var kept []Entry
	found := false
	for _, e := range entries {
		if e.RepoID == repoID {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return false, nil
	}
	return true, write(kept)
}

func write(entries []Entry) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if err := config.EnsureDir(dir); err != nil {
		return err
	}
	p, err := path()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(file{Repos: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	// Owner-only: the registry lists local paths of everything being
	// tracked, same sensitivity as the journal itself.
	return os.WriteFile(p, append(data, '\n'), 0o600)
}
