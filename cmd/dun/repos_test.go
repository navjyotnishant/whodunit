package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
)

func TestReposListWhenNothingInstrumented(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos list: %v", err)
	}
	if !strings.Contains(buf.String(), "no repositories instrumented") {
		t.Errorf("output = %q, want it to say nothing is instrumented", buf.String())
	}
}

func TestReposListShowsInstrumentedRepo(t *testing.T) {
	dir := chdirToTestRepo(t)

	initCmd := newRootCmd()
	initCmd.SetOut(&strings.Builder{})
	initCmd.SetArgs([]string{"init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos list: %v", err)
	}

	// t.TempDir may hand back a symlinked path (/var vs /private/var on
	// macOS), so compare on the leaf rather than the whole path.
	if !strings.Contains(buf.String(), filepath.Base(dir)) {
		t.Errorf("output = %q, want it to list %q", buf.String(), dir)
	}
}

func TestReposListFlagsAMissingPath(t *testing.T) {
	// A repo that was instrumented and later deleted should be shown as
	// gone, not printed as though it were still live.
	chdirToTestRepo(t)

	initCmd := newRootCmd()
	initCmd.SetOut(&strings.Builder{})
	initCmd.SetArgs([]string{"init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Point the registry at a path that does not exist by rewriting it.
	repoID, err := currentRepoID()
	if err != nil {
		t.Fatalf("currentRepoID: %v", err)
	}
	home := os.Getenv("WHODUNIT_HOME")
	regPath := filepath.Join(home, "repos.json")
	content := `{"repos":[{"repo_id":"` + repoID + `","path":"/definitely/not/here","instrumented_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(regPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos list: %v", err)
	}
	if !strings.Contains(buf.String(), "no longer exists") {
		t.Errorf("output = %q, want it to flag the missing path", buf.String())
	}
}

func TestReposRemove(t *testing.T) {
	chdirToTestRepo(t)

	initCmd := newRootCmd()
	initCmd.SetOut(&strings.Builder{})
	initCmd.SetArgs([]string{"init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "remove"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos remove: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "removed from the registry") {
		t.Errorf("output = %q, want confirmation of removal", out)
	}
	// Deregistering must not be mistaken for forgetting.
	if !strings.Contains(out, "journal entries are untouched") {
		t.Errorf("output = %q, want it to say the journal is untouched", out)
	}

	// The hooks must survive removal.
	if _, err := os.Stat(filepath.Join(".git", "hooks", "prepare-commit-msg")); err != nil {
		t.Errorf("repos remove uninstalled the hooks: %v", err)
	}
}

func TestReposRemoveWhenNotRegistered(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "remove"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos remove = %v, want nil for an unregistered repo", err)
	}
	if !strings.Contains(buf.String(), "not in the registry") {
		t.Errorf("output = %q, want it to say the repo was not registered", buf.String())
	}
}

func TestDecodeSlugFindsRealDirectory(t *testing.T) {
	// Claude Code encodes a working directory by replacing separators with
	// dashes, which is lossy when a directory name itself contains a dash.
	// decodeSlug resolves by checking what actually exists.
	parent := t.TempDir()
	withDash := filepath.Join(parent, "my-project")
	if err := os.MkdirAll(withDash, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	slug := claudecode.SlugForCwd(withDash)
	got := decodeSlug(slug)
	if got != withDash {
		t.Errorf("decodeSlug(%q) = %q, want %q", slug, got, withDash)
	}
}

func TestDecodeSlugRejectsNonsense(t *testing.T) {
	if got := decodeSlug("not-a-slug-that-exists-anywhere"); got != "" {
		t.Errorf("decodeSlug on a path that does not exist = %q, want empty", got)
	}
	if got := decodeSlug("no-leading-separator"); got != "" {
		t.Errorf("decodeSlug without a leading dash = %q, want empty", got)
	}
}

func TestReposCandidatesReportsEachRepoOnce(t *testing.T) {
	// An agent run from a subdirectory gets its own transcript directory,
	// but it is the same repository — listing it twice would look like two
	// candidates.
	chdirToTestRepo(t)

	fakeClaudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", fakeClaudeHome)
	projects := filepath.Join(fakeClaudeHome, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A repo, plus a subdirectory inside it.
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")

	sub := filepath.Join(repo, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	for _, p := range []string{repo, sub} {
		slug := claudecode.SlugForCwd(p)
		if err := os.MkdirAll(filepath.Join(projects, slug), 0o755); err != nil {
			t.Fatalf("mkdir slug: %v", err)
		}
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "candidates"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos candidates: %v", err)
	}

	if got := strings.Count(buf.String(), "1 repositor"); got != 1 {
		t.Errorf("output should report exactly 1 repository, got:\n%s", buf.String())
	}
}

func TestReposCandidatesExcludesInstrumented(t *testing.T) {
	chdirToTestRepo(t)

	fakeClaudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", fakeClaudeHome)
	projects := filepath.Join(fakeClaudeHome, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	slug := claudecode.SlugForCwd(cwd)
	if err := os.MkdirAll(filepath.Join(projects, slug), 0o755); err != nil {
		t.Fatalf("mkdir slug: %v", err)
	}

	initCmd := newRootCmd()
	initCmd.SetOut(&strings.Builder{})
	initCmd.SetArgs([]string{"init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	cmd := newRootCmd()
	buf := &strings.Builder{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"repos", "candidates"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repos candidates: %v", err)
	}
	if !strings.Contains(buf.String(), "no uninstrumented repositories") {
		t.Errorf("an instrumented repo must not be listed as a candidate: %s", buf.String())
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
	)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
