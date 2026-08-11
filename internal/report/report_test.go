package report

import (
	"os"
	"os/exec"
	"strings"
	"testing"

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
	if !strings.Contains(out, "pre-adoption baseline") {
		t.Error("missing honest gap note about missing velocity baseline")
	}
}

func TestRenderEscapesUntrustedValues(t *testing.T) {
	s := Stats{TotalCommits: 1, Covered: 1, MethodCount: map[spec.Method]int{}}
	var b strings.Builder
	Render(&b, s)
	if strings.Contains(b.String(), "<script>") {
		t.Error("output should never contain unescaped script tags")
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
