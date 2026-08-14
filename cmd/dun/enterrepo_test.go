// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Commands that read the working directory explain themselves
// outside a repository.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run outside a repository, these surfaced git's exit status:
//
//	Error: resolve repo root commit (unborn or not a git repo?): exit status 128
//
// which names neither the problem nor a way out. The same fault was fixed
// for `dun status` and `dun journal` and missed here, because these resolve
// the repository inside their work rather than at the entry point.
func TestCommandsOutsideARepositoryExplainThemselves(t *testing.T) {
	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"sync", "report", "ingest", "delta"} {
		t.Run(name, func(t *testing.T) {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{name})

			err := root.Execute()
			if err == nil {
				t.Fatalf("%s succeeded outside a repository", name)
			}
			msg := err.Error()

			if strings.Contains(msg, "exit status") {
				t.Errorf("%s surfaced git's exit status instead of explaining: %q", name, msg)
			}
			if !strings.Contains(msg, "--repo") {
				t.Errorf("%s did not mention --repo, so there is no way forward: %q", name, msg)
			}
		})
	}
}

// The flag has to work, not merely be advertised.
func TestRepoFlagRunsAgainstTheNamedRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	repo := newRepo(t, "target")

	outside := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"report", "--repo", repo, "--out", home + "/r.html"})

	if err := root.Execute(); err != nil {
		t.Fatalf("report --repo failed from outside a repository: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(home + "/r.html"); err != nil {
		t.Errorf("no report was written: %v", err)
	}

	// The directory must be restored: a command that leaves the process
	// somewhere else breaks whatever runs next in the same process.
	//
	// Compared through EvalSymlinks because macOS resolves /var to
	// /private/var, so the same directory has two spellings and a string
	// comparison fails on identical paths.
	now, _ := os.Getwd()
	wantDir, _ := filepath.EvalSymlinks(outside)
	gotDir, _ := filepath.EvalSymlinks(now)
	if gotDir != wantDir {
		t.Errorf("working directory left at %q, want %q", gotDir, wantDir)
	}
}

// The message is the whole point of the fix, so its content is asserted
// rather than only its absence of git internals. Someone hitting this is
// usually in the wrong directory, and the difference between an errno and a
// named next command is the difference between a five-second fix and a bug
// report.
func TestNotInRepoErrorNamesTheCommandAndTheWayOut(t *testing.T) {
	msg := notInRepoError("report", "report on").Error()

	for _, want := range []string{
		"not inside a git repository",     // the problem
		"dun report --repo",               // the fix, for this command
		"dun repos list",                  // how to find a path to pass it
		"report on a specific repository", // what the flag will do
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q:\n%s", want, msg)
		}
	}
}

// restore is documented as never nil so callers can defer it without
// checking. A nil would panic at the defer, in the error path — the worst
// place to find one.
func TestEnterRepoAlwaysReturnsARestoreFunction(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		repo string
	}{
		{"no flag, outside a repository", ""},
		{"flag naming a missing path", filepath.Join(dir, "missing")},
		{"flag naming a plain directory", dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore, _ := enterRepo(tc.repo, "report", "report on")
			if restore == nil {
				t.Fatal("restore is nil")
			}
			restore() // must not panic
		})
	}
}

// Every command operating on "the current repository" has to go through
// enterRepo, or it reintroduces this bug for itself — which is how report,
// delta, ingest and sync acquired it after status and journal were fixed.
// They resolve the repository inside their work, so nothing about the
// command's own code makes the omission visible.
//
// The behavioural test above covers these four by running them. This adds
// the reason: a failure here names enterRepo, so the next person knows what
// to reach for rather than rediscovering it.
func TestCommandsOperatingOnTheCurrentRepositoryUseEnterRepo(t *testing.T) {
	for file, command := range map[string]string{
		"report.go": "report",
		"delta.go":  "delta",
		"ingest.go": "ingest",
		"sync.go":   "sync",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if !strings.Contains(string(src), `enterRepo(repoFlag, "`+command+`"`) {
			t.Errorf("%s no longer calls enterRepo — `dun %s` outside a repository "+
				"will surface git's exit status instead of a usable message", file, command)
		}
	}
}
