package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDirInRealRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	gd, err := gitDir()
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	if !strings.HasSuffix(gd, ".git") {
		t.Errorf("gitDir() = %q, want it to end in .git", gd)
	}
}

func TestGitDirOutsideRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if _, err := gitDir(); err == nil {
		t.Error("gitDir() = nil error outside a git repo, want error")
	}
}

func TestJournalDirIsUnderGitDir(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	jd, err := journalDir()
	if err != nil {
		t.Fatalf("journalDir: %v", err)
	}
	want := filepath.Join("dun", "journal")
	if !strings.HasSuffix(jd, want) {
		t.Errorf("journalDir() = %q, want it to end in %q", jd, want)
	}
	if !strings.Contains(jd, ".git") {
		t.Errorf("journalDir() = %q, want it under .git (outside the worktree)", jd)
	}
}
