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
