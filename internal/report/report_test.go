package report

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/purpose"
	"github.com/navjyotnishant/whodunit/internal/spec"
)

func TestCoverageAndPenetration(t *testing.T) {
	s := Stats{
		TotalCommits: 10,
		Covered:      8,
		Assisted:     5,
	}
	if got := s.Coverage(); got != 0.8 {
		t.Errorf("Coverage() = %v, want 0.8", got)
	}
	if got := s.Penetration(); got != 0.625 {
		t.Errorf("Penetration() = %v, want 0.625 (5/8, undetermined excluded)", got)
	}
}

func TestCoverageZeroCommits(t *testing.T) {
	s := Stats{}
	if got := s.Coverage(); got != 0 {
		t.Errorf("Coverage() on empty stats = %v, want 0", got)
	}
	if got := s.Penetration(); got != 0 {
		t.Errorf("Penetration() on empty stats = %v, want 0", got)
	}
}

func TestRenderProducesValidStructure(t *testing.T) {
	s := Stats{
		TotalCommits: 5,
		Covered:      3,
		Assisted:     2,
		MethodCount:  map[spec.Method]int{spec.MethodObserved: 2, spec.MethodUndetermined: 1},
		PurposeCount: map[purpose.Purpose]int{purpose.Feature: 3, purpose.Test: 2},
	}
	var b strings.Builder
	Render(&b, s)
	out := b.String()

	if !strings.Contains(out, "<!doctype html>") {
		t.Error("missing doctype")
	}
	if !strings.Contains(out, "60%") { // coverage 3/5
		t.Errorf("missing coverage figure: %s", out)
	}
	if !strings.Contains(out, "observed") {
		t.Error("missing method mix row")
	}
	if !strings.Contains(out, "feature") {
		t.Error("missing purpose breakdown row")
	}
	if !strings.Contains(out, "<svg") {
		t.Error("missing method mix chart")
	}
	if !strings.Contains(out, "pre-adoption baseline") {
		t.Error("missing honest gap note about missing velocity baseline")
	}
}

func TestRenderIncludesCommitTable(t *testing.T) {
	tr := spec.Trailer{Status: spec.StatusAssisted, Method: spec.MethodObserved}
	s := Stats{
		TotalCommits: 1,
		Covered:      1,
		Assisted:     1,
		MethodCount:  map[spec.Method]int{spec.MethodObserved: 1},
		PurposeCount: map[purpose.Purpose]int{purpose.Feature: 1},
		Commits: []Commit{
			{SHA: "abcdef1234567890", Subject: "feat: add thing", Trailer: &tr, Purpose: purpose.Feature},
		},
	}
	// The commit table lives in the detail template. The default (exec)
	// answers "is adoption growing", which a per-commit list does not.
	var b strings.Builder
	RenderTemplate(&b, s, Activity{}, TemplateDetail)
	out := b.String()

	if !strings.Contains(out, "abcdef12") {
		t.Errorf("missing short sha in commit table: %s", out)
	}
	if !strings.Contains(out, "add thing") {
		t.Errorf("missing commit subject: %s", out)
	}
}

func TestRenderEscapesUntrustedValues(t *testing.T) {
	s := Stats{
		TotalCommits: 1,
		Covered:      1,
		MethodCount:  map[spec.Method]int{},
		PurposeCount: map[purpose.Purpose]int{},
		Commits: []Commit{
			{SHA: "abc", Subject: "<script>alert(1)</script>", Purpose: purpose.Other},
		},
	}
	var b strings.Builder
	Render(&b, s)
	if strings.Contains(b.String(), "<script>") {
		t.Error("output should never contain unescaped script tags, even from a commit subject")
	}
}

func TestCollectOnEmptyRepoDoesNotError(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	stats, err := Collect(500)
	if err != nil {
		t.Fatalf("Collect() on empty repo = %v, want nil error", err)
	}
	if stats.TotalCommits != 0 {
		t.Errorf("TotalCommits = %d, want 0", stats.TotalCommits)
	}
}

func TestCollectAgainstRealRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, content string) {
		t.Helper()
		full := dir + "/" + path
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	run("init", "-q")

	write("main.go", "package main\n")
	run("add", "main.go")
	run("commit", "-q", "-m", "feat: add main\n\nAI-Attribution: status=assisted; method=observed; agent=claude-code; agent_version=1.0.0")

	write("main_test.go", "package main\n")
	run("add", "main_test.go")
	run("commit", "-q", "-m", "test: cover main")

	write("README.md", "# repo\n")
	run("add", "README.md")
	run("commit", "-q", "-m", "manual commit with no trailer")

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	stats, err := Collect(500)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if stats.TotalCommits != 3 {
		t.Fatalf("TotalCommits = %d, want 3", stats.TotalCommits)
	}
	if stats.Covered != 1 {
		t.Errorf("Covered = %d, want 1 (only the first commit has a trailer)", stats.Covered)
	}
	if stats.Assisted != 1 {
		t.Errorf("Assisted = %d, want 1", stats.Assisted)
	}
	if len(stats.Commits) != 3 {
		t.Fatalf("len(Commits) = %d, want 3", len(stats.Commits))
	}

	byPurpose := map[purpose.Purpose]int{}
	for _, c := range stats.Commits {
		byPurpose[c.Purpose]++
	}
	if byPurpose[purpose.Feature] != 1 {
		t.Errorf("purpose feature count = %d, want 1", byPurpose[purpose.Feature])
	}
	if byPurpose[purpose.Test] != 1 {
		t.Errorf("purpose test count = %d, want 1", byPurpose[purpose.Test])
	}

	// The trailer-carrying commit must have Files populated and a non-nil Trailer.
	var found bool
	for _, c := range stats.Commits {
		if c.Trailer != nil && c.Trailer.Method == spec.MethodObserved {
			found = true
			if len(c.Files) != 1 || c.Files[0] != "main.go" {
				t.Errorf("commit files = %v, want [main.go]", c.Files)
			}
		}
	}
	if !found {
		t.Error("no commit found with the expected observed trailer")
	}
}
