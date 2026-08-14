package delta

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/baseline"
	"github.com/navjyotnishant/whodunit/internal/purpose"
)

const assistedTrailer = "AI-Attribution: status=assisted; method=observed; agent=claude-code; agent_version=1.0.0"

func repoWithMixedCommits(t *testing.T) string {
	t.Helper()
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
	commit := func(file, content, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
		run("add", file)
		run("commit", "-q", "-m", msg)
	}

	run("init", "-q")
	commit("a.go", "package a\nvar A = 1\n", "feat: add a\n\n"+assistedTrailer)
	commit("b.go", "package b\nvar B = 2\n", "feat: add b\n\n"+assistedTrailer)
	commit("c.go", "package c\n", "feat: add c") // no trailer -> undetermined
	commit("d.go", "package d\n", `Revert "feat: add c"`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return dir
}

func TestComputeSplitsAssistedFromUndetermined(t *testing.T) {
	repoWithMixedCommits(t)

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if res.Within.Assisted.Commits != 2 {
		t.Errorf("assisted commits = %d, want 2", res.Within.Assisted.Commits)
	}
	if res.Within.Undetermined.Commits != 2 {
		t.Errorf("undetermined commits = %d, want 2 (no trailer, and the revert)", res.Within.Undetermined.Commits)
	}
}

func TestComputeFindsTrailerAfterMultiLineBody(t *testing.T) {
	// Regression: a line-oriented parse of %s + %b sees only the first body
	// line, so a trailer at the END of a long message is missed and every
	// assisted commit is silently counted as undetermined.
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
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "feat: a thing\n\nA long explanation.\n\nWith several paragraphs, so the trailer is\nnowhere near the first body line.\n\n"+assistedTrailer)

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Within.Assisted.Commits != 1 {
		t.Errorf("assisted commits = %d, want 1 — trailer after a multi-line body was missed",
			res.Within.Assisted.Commits)
	}
}

func TestComputeCountsRevertsInTheRightGroup(t *testing.T) {
	repoWithMixedCommits(t)

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Within.Undetermined.Reverts != 1 {
		t.Errorf("undetermined reverts = %d, want 1", res.Within.Undetermined.Reverts)
	}
	if res.Within.Assisted.Reverts != 0 {
		t.Errorf("assisted reverts = %d, want 0", res.Within.Assisted.Reverts)
	}
}

func TestComputeWarnsWithoutBaseline(t *testing.T) {
	repoWithMixedCommits(t)

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Cross != nil {
		t.Error("Cross should be nil when no baseline was supplied")
	}
	if !containsSubstring(res.Warnings, "no pre-adoption baseline") {
		t.Errorf("warnings should say the baseline is missing: %v", res.Warnings)
	}
}

func TestComputeWarnsOnThinData(t *testing.T) {
	repoWithMixedCommits(t) // only 4 commits total

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !containsSubstring(res.Warnings, "thin data") {
		t.Errorf("a 4-commit repo must be flagged as thin data: %v", res.Warnings)
	}
}

func TestComputeIncludesConfoundersWithBaseline(t *testing.T) {
	repoWithMixedCommits(t)

	base := &baseline.Snapshot{
		SchemaVersion: baseline.SchemaVersion,
		CapturedAt:    time.Now().AddDate(0, 0, -120),
		WindowDays:    90,
		Git: baseline.GitMetrics{
			Commits:             50,
			CommitsPerWeek:      3.9,
			MedianDiffLines:     40,
			Reverts:             2,
			RevertRate:          0.04,
			PurposeDistribution: map[purpose.Purpose]int{purpose.Feature: 50},
		},
	}

	res, err := Compute(base, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Cross == nil {
		t.Fatal("Cross should be populated when a baseline is supplied")
	}
	if len(res.Cross.Confounders) == 0 {
		t.Error("a cross-period comparison must always carry its confounders")
	}
	if res.Cross.Baseline.Commits != 50 {
		t.Errorf("baseline commits = %d, want 50", res.Cross.Baseline.Commits)
	}
}

func TestComputeWarnsOnWindowMismatch(t *testing.T) {
	repoWithMixedCommits(t)

	base := &baseline.Snapshot{
		SchemaVersion: baseline.SchemaVersion,
		CapturedAt:    time.Now().AddDate(0, 0, -200),
		WindowDays:    30, // measured a different window than we're comparing against
		Git:           baseline.GitMetrics{Commits: 10, PurposeDistribution: map[purpose.Purpose]int{}},
	}

	res, err := Compute(base, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !containsSubstring(res.Warnings, "window mismatch") {
		t.Errorf("comparing a 30-day baseline to a 90-day window must warn: %v", res.Warnings)
	}
}

func TestPercentChangeFromZeroIsUndefined(t *testing.T) {
	// A change from zero is undefined, not infinite, and must never be
	// rendered as a number.
	if _, ok := PercentChange(0, 5); ok {
		t.Error("PercentChange(0, 5) reported a value; change from zero is undefined")
	}
	pct, ok := PercentChange(4, 5)
	if !ok || pct < 0.24 || pct > 0.26 {
		t.Errorf("PercentChange(4, 5) = %v, %v; want ~0.25, true", pct, ok)
	}
}

func TestComputeOnEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	res, err := Compute(nil, 90, time.Now())
	if err != nil {
		t.Fatalf("Compute on empty repo = %v, want nil error", err)
	}
	if res.Within.Assisted.Commits != 0 || res.Within.Undetermined.Commits != 0 {
		t.Errorf("empty repo should yield zero commits, got %+v", res.Within)
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
