package purpose

import "testing"

func TestClassifyByConventionalCommit(t *testing.T) {
	cases := []struct {
		msg  string
		want Purpose
	}{
		{"feat: add auth", Feature},
		{"fix: null pointer", Fix},
		{"fix(hook): check the real PR head commit", Fix},
		{"docs: add README, set repo description", Docs},
		{"ci: restrict releases to PRD only", Config},
		{"test: cover remaining cmd/dun files", Test},
		{"chore: bump deps", Chore},
		{"refactor!: rename package", Refactor},
	}
	for _, c := range cases {
		got := Classify(c.msg, nil)
		if got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestClassifyFallsBackToPaths(t *testing.T) {
	cases := []struct {
		name  string
		msg   string
		files []string
		want  Purpose
	}{
		{"go.mod only", "bump sqlite", []string{"go.mod", "go.sum"}, Dependency},
		{"test files only", "cover the parser", []string{"internal/spec/trailer_test.go"}, Test},
		{"docs only", "clarify install steps", []string{"README.md"}, Docs},
		{"workflow only", "tweak trigger", []string{".github/workflows/release.yml"}, Config},
		{"mixed source+test falls through", "add feature", []string{"internal/spec/trailer.go", "internal/spec/trailer_test.go"}, Other},
	}
	for _, c := range cases {
		got := Classify(c.msg, c.files)
		if got != c.want {
			t.Errorf("%s: Classify(%q, %v) = %v, want %v", c.name, c.msg, c.files, got, c.want)
		}
	}
}

func TestClassifyFallsBackToDiffShape(t *testing.T) {
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	got := Classify("update stuff", files)
	if got != Refactor {
		t.Errorf("Classify with 6 unrelated files = %v, want Refactor (broad-change heuristic)", got)
	}
}

func TestClassifyNoSignalIsOther(t *testing.T) {
	got := Classify("update stuff", []string{"internal/attribution/attribution.go"})
	if got != Other {
		t.Errorf("Classify with no signal = %v, want Other, not a guess", got)
	}
}

func TestClassifyEmptyMessageAndFiles(t *testing.T) {
	got := Classify("", nil)
	if got != Other {
		t.Errorf("Classify(\"\", nil) = %v, want Other", got)
	}
}
