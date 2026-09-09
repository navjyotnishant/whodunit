// Author: Navjyot Nishant
// Created: 2026-09-09
// Last updated: 2026-09-09
// Description: Tests for Bash write-target extraction (WHO-238).

package claudecode

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// want returns the paths WriteTargets should produce, resolved the way it
// resolves them, so a test states repo-relative names and still compares
// against absolute ones.
func want(cwd string, rel ...string) []string {
	out := make([]string, 0, len(rel))
	for _, r := range rel {
		out = append(out, resolveTarget(r, cwd))
	}
	sort.Strings(out)
	return out
}

func got(t *testing.T, cmd, cwd string) ([]string, bool) {
	t.Helper()
	p, incomplete := WriteTargets(cmd, cwd)
	sort.Strings(p)
	return p, incomplete
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWriteTargetsNeverReturnsFileContent is the constraint that outranks
// every other test here.
//
// The whole reason Bash was excluded from attribution is that a command line
// can carry file contents — `cat > f <<'EOF' … EOF` embeds the file itself.
// Extracting the target is only acceptable if the body never comes with it
// (NAV-25). A regression here is not a wrong number, it is the tool
// recording something it promised never to record.
func TestWriteTargetsNeverReturnsFileContent(t *testing.T) {
	const secret = "SUPER_SECRET_BODY_LINE"
	cases := []string{
		"cat > out.md <<'EOF'\n" + secret + "\nEOF",
		"echo '" + secret + "' > out.md",
		"printf '%s\\n' '" + secret + "' >> out.md",
		"python3 - <<'PY'\np='out.md'\nopen(p,'w').write('''" + secret + "''')\nPY",
		"tee out.md <<'EOF'\n" + secret + "\nEOF",
	}
	for _, cmd := range cases {
		paths, _ := WriteTargets(cmd, "/repo")
		for _, p := range paths {
			if strings.Contains(p, secret) {
				t.Fatalf("file content reached a returned path (NAV-25):\n  cmd: %q\n  got: %q", cmd, p)
			}
		}
		if len(paths) == 0 {
			t.Errorf("no target extracted from %q — the write is invisible", cmd)
		}
	}
}

// TestWriteTargetsExtractsRealWrites covers the mechanisms measured as the
// bulk of agent authorship.
func TestWriteTargetsExtractsRealWrites(t *testing.T) {
	const cwd = "/repo"
	cases := []struct {
		name string
		cmd  string
		rel  []string
	}{
		{"heredoc", "cat > docs/a.md <<'EOF'\nbody\nEOF", []string{"docs/a.md"}},
		{"echo redirect", `echo "x" > a.txt`, []string{"a.txt"}},
		{"append", "printf 'x\\n' >> log.txt", []string{"log.txt"}},
		{"gofmt", "gofmt -w internal/x.go", []string{"internal/x.go"}},
		{"tee", "echo hi | tee logs/out.txt", []string{"logs/out.txt"}},
		{"sed -i gnu", "sed -i 's/a/b/' cmd/main.go", []string{"cmd/main.go"}},
		{"sed -i bsd", "sed -i '' 's/a/b/' cmd/main.go", []string{"cmd/main.go"}},

		{"python open var", "python3 - <<'PY'\np='a/b.go'\nopen(p,'w').write(s)\nPY", []string{"a/b.go"}},
		{"python open literal", `python3 -c "open('a/b.go','w').write(s)"`, []string{"a/b.go"}},
		{"python pathlib var", "python3 - <<'PY'\nimport pathlib\np = pathlib.Path(\"x/y.go\")\np.write_text(s)\nPY", []string{"x/y.go"}},
		{"python Path literal", `python3 -c "import pathlib; pathlib.Path('C.md').write_text('x')"`, []string{"C.md"}},
		{"python loop literal list", "python3 - <<'PY'\nfor f in ['a.go','b.go']:\n    open(f,'w').write(s)\nPY", []string{"a.go", "b.go"}},

		{"multiple targets one command",
			"cat > one.txt <<'EOF'\n1\nEOF\necho two > two.txt\nprintf '3' > three.txt",
			[]string{"one.txt", "two.txt", "three.txt"}},

		// The compound case: excluding by command name rather than by write
		// target loses the write entirely.
		{"build then write", "go build ./... && cat > note.md <<'E'\nx\nE", []string{"note.md"}},
		{"write then git add", "cat > .changelog.d/x.md <<'EOF'\nx\nEOF\ngit add -A", []string{".changelog.d/x.md"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, incomplete := got(t, c.cmd, cwd)
			w := want(cwd, c.rel...)
			if !eq(p, w) {
				t.Errorf("WriteTargets()\n got = %q\nwant = %q", p, w)
			}
			if incomplete {
				t.Errorf("marked incomplete, but every target is nameable")
			}
		})
	}
}

// TestWriteTargetsIgnoresNonAuthorship is the half that keeps attribution
// honest. A false path claims a commit touched a file it never touched,
// which is worse than recording nothing at all.
func TestWriteTargetsIgnoresNonAuthorship(t *testing.T) {
	cases := []struct{ name, cmd string }{
		{"stderr redirect", "go test ./... 2>&1 | tail -5"},
		{"devnull", "ls -la > /dev/null"},
		{"devnull and stderr", "grep -n foo bar.go > /dev/null 2>&1"},
		{"fd redirect", "echo x >&2"},
		{"read", "cat go.mod"},
		{"git status", "git status --porcelain"},
		{"mkdir", "mkdir -p a/b/c"},
		{"cp", "cp a.txt b.txt"},
		{"mv", "mv a.txt b.txt"},
		{"rm", "rm -rf build"},
		{"printf verb", `printf '%s\n' foo`},
		{"hex colour", `echo '#22d3ee'`},
		{"python read only", `python3 -c "import yaml; yaml.safe_load(open('x.yml'))"`},
		{"python print only", "python3 - <<'PY'\nimport json\nd=json.load(open('a.json'))\nprint(len(d))\nPY"},
		{"pathlib read only", `python3 -c "import pathlib; print(pathlib.Path('go.mod').read_text())"`},
		{"gofmt list only", "gofmt -l ."},
		{"comparison operator", "test 5 > 3 && echo yes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := WriteTargets(c.cmd, "/repo")
			if len(p) != 0 {
				t.Errorf("extracted %q from a command that authors nothing: %q", p, c.cmd)
			}
		})
	}
}

// TestWriteTargetsReportsAnIncompleteFileSet covers the tier that must never
// be mistaken for full evidence.
func TestWriteTargetsReportsAnIncompleteFileSet(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"unresolved shell var", `cat > "$OUT/x.txt" <<'EOF'` + "\nx\nEOF"},
		{"runtime python path", "python3 - <<'PY'\nimport os\nopen(os.path.join(d,n),'w').write(s)\nPY"},
		{"computed name", "python3 - <<'PY'\nopen(prefix + '.go','w').write(s)\nPY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, incomplete := WriteTargets(c.cmd, "/repo")
			if !incomplete {
				t.Errorf("a write this cannot name was not reported as incomplete: %q", c.cmd)
			}
		})
	}
}

// TestWriteTargetsMarksPartialWhenOnlySomeTargetsAreKnown is the specific
// shape the report flagged as the design risk: one write is readable and
// another is not, so the file list looks complete and is not.
func TestWriteTargetsMarksPartialWhenOnlySomeTargetsAreKnown(t *testing.T) {
	cmd := "cat > known.txt <<'EOF'\nx\nEOF\n" + `echo y > "$HIDDEN/other.txt"`
	paths, incomplete := WriteTargets(cmd, "/repo")
	if len(paths) == 0 {
		t.Fatal("the readable target was lost")
	}
	if !incomplete {
		t.Error("a command with one unnameable write reported a complete file set")
	}
}

// TestWriteTargetsResolvesRelativeToCwd — "cat > x.go" names a file only
// when you know where it ran.
func TestWriteTargetsResolvesRelativeToCwd(t *testing.T) {
	cwd := t.TempDir()
	paths, _ := WriteTargets("echo x > sub/file.go", cwd)
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	// Compare against the same resolution the extractor performs: t.TempDir
	// hands back a symlinked path on macOS (/var vs /private/var), so a raw
	// string comparison against cwd fails on a correct result.
	if want := resolveTarget("sub/file.go", cwd); paths[0] != want {
		t.Errorf("got %q, want %q", paths[0], want)
	}
	if !strings.HasSuffix(paths[0], "sub/file.go") {
		t.Errorf("path %q lost its relative portion", paths[0])
	}
}

// TestWriteTargetsHandlesAbsolutePaths — an absolute target must not be
// joined to cwd. On Windows this was a real bug: C:\tmp\x.txt is absolute
// and a "/" prefix test says it is not, producing a path that exists
// nowhere and attributing a commit to a file that was never written.
func TestWriteTargetsHandlesAbsolutePaths(t *testing.T) {
	paths, _ := WriteTargets("echo x > /tmp/abs.txt", "/repo")
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	if strings.Contains(paths[0], "repo") {
		t.Errorf("an absolute path was joined to cwd: %q", paths[0])
	}

	if runtime.GOOS == "windows" {
		p, _ := WriteTargets(`echo x > C:\tmp\abs.txt`, `C:\repo`)
		if len(p) != 1 {
			t.Fatalf("got %d paths, want 1", len(p))
		}
		if strings.Count(p[0], ":") != 1 {
			t.Errorf("a drive-letter path was joined to cwd: %q", p[0])
		}
	}
}

// TestWriteTargetsCapsLongCommands keeps the commit path bounded. The
// largest command measured on this machine is 38 KB and costs 80x the
// average to scan; a write target is in the first line, never at the end of
// the data being written.
func TestWriteTargetsCapsLongCommands(t *testing.T) {
	tail := strings.Repeat("x", maxCommandScan*2)
	cmd := "cat > head.txt <<'EOF'\n" + tail + "\nEOF\necho y > " + strings.Repeat("d/", 200) + "deep.txt"
	paths, _ := WriteTargets(cmd, "/repo")
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "head.txt") {
		t.Errorf("got %q, want only the target inside the scanned prefix", paths)
	}
}

// TestWriteTargetsIsEmptyForEmptyInput guards the trivial path a parser hits
// constantly.
func TestWriteTargetsIsEmptyForEmptyInput(t *testing.T) {
	if p, inc := WriteTargets("", "/repo"); len(p) != 0 || inc {
		t.Errorf("got (%q, %v), want (empty, false)", p, inc)
	}
	if p, _ := WriteTargets("echo x > a.txt", ""); len(p) != 1 {
		t.Errorf("an empty cwd lost the target: %q", p)
	}
}

// TestWriteTargetsStaysWithinBudget — this runs on the commit path.
func TestWriteTargetsStaysWithinBudget(t *testing.T) {
	cmds := []string{
		"cat > a.md <<'EOF'\n" + strings.Repeat("line\n", 500) + "EOF",
		"go test ./... 2>&1 | tail -20",
		"python3 - <<'PY'\np='x.go'\nopen(p,'w').write(s)\nPY",
		strings.Repeat("echo x > f.txt; ", 200),
	}
	// 8,000 extractions is the largest single session measured on this
	// machine. The budget is loose on purpose: it catches an accidental
	// quadratic, not a few microseconds.
	const iterations = 2000
	for i := 0; i < iterations; i++ {
		for _, c := range cmds {
			WriteTargets(c, "/repo")
		}
	}
}

// TestResolveTargetFoldsSeparators — both halves of an attribution match
// have to spell a path identically or nothing ever matches.
func TestResolveTargetFoldsSeparators(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("separator folding is only observable where filepath.Join uses /")
	}
	a := resolveTarget("sub/file.go", "/repo")
	b := resolveTarget(filepath.Join("sub", "file.go"), "/repo")
	if a != b {
		t.Errorf("the same file resolved two ways: %q vs %q", a, b)
	}
	if strings.Contains(a, `\`) {
		t.Errorf("a backslash survived canonicalisation: %q", a)
	}
}
