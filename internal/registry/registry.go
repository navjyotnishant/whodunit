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

	// Written to a temporary file and renamed over the target, the same
	// discipline hooklog.rewrite and journal.Backup already use.
	//
	// os.WriteFile truncates before it writes, so a reader arriving in that
	// window sees a partial file and List fails to unmarshal it — measured
	// at 7 of 100 reads with one concurrent writer. That takes every
	// cross-repo command down, and this file is not regenerable: it is the
	// list of repositories someone chose to instrument, and rebuilding it
	// means re-running `dun init` in each one, if they even realise that is
	// what happened.
	//
	// Rename is atomic on the same filesystem, so a reader sees either the
	// old file or the new one.
	//
	// Owner-only: the registry lists local paths of everything being
	// tracked, same sensitivity as the journal itself.
	tmp, err := os.CreateTemp(dir, ".repos-*.json")
	if err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	// Flushed before the rename: the rename is atomic over bytes that
	// reached the filesystem, not over bytes still in a buffer.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return renameOver(tmpName, p)
}

// renameOver replaces dst with src, retrying while the target is held open.
//
// On Unix rename(2) replaces an existing file unconditionally, and this is
// one call. Windows is the reason for the loop: MoveFileEx is given
// MOVEFILE_REPLACE_EXISTING by Go already, but it still fails with "Access
// is denied" while another process has the target open for reading — and a
// reader is exactly what this is protecting, so the collision is expected
// rather than exceptional.
//
// Retried briefly rather than reported. A read takes microseconds, so
// waiting one out is nearly free, and the alternative is failing `dun init`
// because someone happened to run `dun status` at the same moment.
//
// If it still will not go through, the error is returned: the temporary
// file is cleaned up by the caller's defer, the old registry is intact, and
// the operation is reported as failed rather than silently dropped.
func renameOver(src, dst string) error {
	// Backs off up to roughly a second in total. Long enough to outlast any
	// read of a file this size, short enough that a genuinely stuck target
	// is reported rather than hung on.
	var err error
	for attempt := 0; attempt < 12; attempt++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		time.Sleep(time.Duration(5*(attempt+1)*(attempt+1)) * time.Millisecond)
	}
	return fmt.Errorf("write registry: could not replace %s after 12 attempts "+
		"(another process may be holding it open): %w", dst, err)
}
