package testmode_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `make perf` selects the performance gates by name, with a -run pattern.
// That is fragile in one specific way: a timing test whose name matches
// nothing is not reported as skipped — it simply never runs, and the gate
// silently covers less than it appears to.
//
// This already happened. TestParseSinceScalesLinearly did not match a
// pattern of "Budget|Perf|Scaling", so two new timing tests passed locally
// and would never have run in CI.
//
// So the convention is enforced rather than documented: a test that
// asserts a duration must be named so `make perf` selects it.
var perfPattern = regexp.MustCompile(
	`Budget|Perf|Scales|Cheaply|CriticalPath|FailsFast|DoesNotSlow`)

// A test is treated as a timing test when it compares against time.Since
// or a Duration budget. Deliberately a source-level heuristic: the
// alternative is a registry someone has to remember to update, which is
// the same failure in a different place.
func TestEveryTimingTestIsSelectedByMakePerf(t *testing.T) {
	root := repoRoot(t)

	var missed []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if name := info.Name(); name == ".git" || name == "dist" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil // a file that does not parse is the compiler's problem
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if !assertsOnDuration(fset, fn, path) {
				continue
			}
			if !perfPattern.MatchString(fn.Name.Name) {
				rel, _ := filepath.Rel(root, path)
				missed = append(missed, rel+": "+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(missed) > 0 {
		t.Errorf("these tests assert on elapsed time but are not selected by `make perf`,\n"+
			"so they never run as a gate. Rename them to match %s:\n  %s",
			perfPattern, strings.Join(missed, "\n  "))
	}
}

// assertsOnDuration reports whether a function both measures elapsed time
// and compares it against something. Measuring alone is not enough — a
// test may time an operation only to log it.
func assertsOnDuration(fset *token.FileSet, fn *ast.FuncDecl, path string) bool {
	src, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	start := fset.Position(fn.Pos()).Offset
	end := fset.Position(fn.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return false
	}
	body := string(src[start:end])

	measures := strings.Contains(body, "time.Since(")
	compares := strings.Contains(body, "budget") ||
		strings.Contains(body, "Budget") ||
		strings.Contains(body, "ceiling")
	return measures && compares
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}
