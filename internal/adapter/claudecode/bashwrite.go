// Author: Navjyot Nishant
// Created: 2026-09-09
// Last updated: 2026-09-09
// Description: Extracts the files a Bash command writes, from the command
// text alone, without retaining any of the content it writes.

package claudecode

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// WriteTargets returns the files a Bash command authors, resolved against
// cwd, and whether the command performs a write it could not name.
//
// # Why this exists
//
// An agent that edits through Edit/Write is recorded with a file. An agent
// that runs `cat > main.go <<'EOF'` was recorded as a bare tool call with no
// file at all, so the same edit was invisible to attribution and the commit
// resolved unmatched. On this machine's journal that was 7,263 Bash records
// against 894 Edit/Write ones — the majority of agent authorship, unseen
// (WHO-238).
//
// # What it does NOT keep
//
// The target only. Never the heredoc body, never the string being written,
// never the command itself. A path is a name; the bytes after `<<'EOF'` are
// file content and stay out of the journal by construction (NAV-25). The
// caller passes the command in and gets paths out; nothing else escapes.
//
// # The rule, and why it is written this way
//
// A command counts only if a WRITE OPERATOR NAMES A TARGET. Not "the command
// mentions a file" — `ls -l x.go` mentions one and authors nothing. Not "the
// command is not a git command" either: `cat > f <<'EOF' … && git add -A`
// authors a file and runs git, and excluding on the command name drops the
// write. The report this came from measured that mistake at 181 lost writes.
//
// Deliberately absent: mkdir (creates a directory, authors no content),
// cp/mv (moves content authored elsewhere and already recorded there), and
// every read.
//
// # Accuracy
//
// Measured over 21,229 real Bash calls across five repositories: 96.1% of
// commands that write name a target this can extract, and 90% of extracted
// paths that still exist on disk are source files. The residual 3.9% build
// their path at runtime — `open(os.path.join(d, n), 'w')` — and are reported
// through the `incomplete` return rather than guessed at. A partial file set
// that looks complete is the wrong-number-that-reads-as-a-finding this
// project exists to avoid, so the caller must not treat it as full evidence.
func WriteTargets(command, cwd string) (paths []string, incomplete bool) {
	if command == "" {
		return nil, false
	}
	// A write target appears in the first line of a command, never at
	// character 38,000 — the tail of a long command is the data being
	// written. Capping bounds the regex work on the 0.3% of commands that
	// are huge, and this runs on the commit path.
	if len(command) > maxCommandScan {
		command = command[:maxCommandScan]
	}

	found := map[string]bool{}

	for _, rule := range shellWriteRules {
		for _, m := range rule.pattern.FindAllStringSubmatch(command, -1) {
			target := m[1]
			switch {
			case devNull.MatchString(target), strings.HasPrefix(target, "&"):
				// 2>&1 and >/dev/null are not files.
			case strings.ContainsAny(target, "$`"):
				// A path built from a variable this cannot resolve. Say so
				// rather than record a literal "$OUT".
				incomplete = true
			case !plausiblePath.MatchString(target):
				// A printf verb (%s), a hex colour (#e8edf3), a bare word.
				// Without this guard an early version of this function
				// produced 591 junk paths out of 1,128.
			default:
				found[target] = true
			}
		}
	}

	if pythonWrites.MatchString(command) {
		before := len(found)
		vars := pythonPathVars(command)

		for _, m := range pyOpenVar.FindAllStringSubmatch(command, -1) {
			if p, ok := vars[m[1]]; ok {
				found[p] = true
			}
		}
		for _, m := range pyOpenLiteral.FindAllStringSubmatch(command, -1) {
			found[m[1]] = true
		}
		for _, m := range pyPathLiteralWrite.FindAllStringSubmatch(command, -1) {
			found[m[1]] = true
		}
		for _, m := range pyVarWrite.FindAllStringSubmatch(command, -1) {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if p, ok := vars[name]; ok {
				found[p] = true
			}
		}

		// A loop over a literal list, whose body writes: unroll it.
		if len(found) == before {
			for _, m := range pyLiteralList.FindAllStringSubmatch(command, -1) {
				for _, lit := range pyStringLiteral.FindAllStringSubmatch(m[1], -1) {
					if plausiblePath.MatchString(lit[1]) {
						found[lit[1]] = true
					}
				}
			}
		}

		// Python wrote something and named nothing this understands.
		if len(found) == before {
			incomplete = true
		}
	}

	for target := range found {
		paths = append(paths, resolveTarget(target, cwd))
	}
	return paths, incomplete
}

// maxCommandScan bounds how much of a command is examined. Measured over
// 8,071 commands: the median is 342 characters and 0.29% exceed this, but
// the largest is 38 KB and costs 80x the average to scan.
const maxCommandScan = 8000

// resolveTarget makes a target absolute and canonical.
//
// filepath.IsAbs rather than a "/" prefix test, because C:\tmp\out.txt is
// absolute and a prefix test says otherwise — joining it to cwd produced
// C:\Users\me\repo/C:\tmp\out.txt, a path that exists nowhere. A wrong path
// is worse than a missing one: it attributes a commit to a file the agent
// never touched.
//
// Canonical last, so both halves of an attribution match spell the path the
// same way. It folds separators, which is what makes src\app.go and
// src/app.go the same file on Windows.
func resolveTarget(target, cwd string) string {
	if !filepath.IsAbs(target) && cwd != "" {
		target = filepath.Join(cwd, target)
	}
	return linehash.Canonical(target)
}

type shellWriteRule struct {
	method  string
	pattern *regexp.Regexp
}

// shellWriteRules is the per-mechanism table. It is a table rather than one
// expression because it is not universal and will need extending: `gofmt -w`
// is 8.5% of writes in a Go repository and 0% in a Python one, where the
// equivalents are `black` and `ruff format`. An unrecognised mechanism costs
// attribution, never correctness — the command yields no path rather than a
// wrong one.
var shellWriteRules = []shellWriteRule{
	// > file and >> file. The lookbehind rejects 2> and &>, and the leading
	// boundary stops a '>' inside a quoted string from matching.
	// The optional quote matters: `> "$OUT/x.txt"` is a real write, and
	// excluding the quote character made it match nothing at all — so it
	// was neither extracted nor reported as unresolvable, which is the
	// silent half of the failure this whole change exists to remove.
	{"shell-redirect", regexp.MustCompile(`(?:^|[\s;|&(])>{1,2}\s*["']?([^\s|;&<>'"` + "`" + `()]+)`)},
	{"gofmt", regexp.MustCompile(`\bgofmt\s+-w\s+([^\s;|&]+)`)},
	{"tee", regexp.MustCompile(`\btee\s+(?:-a\s+)?([^\s;|&]+)`)},
	{"sed-i", regexp.MustCompile(`\bsed\s+-i(?:\s+'')?\s+(?:-e\s+)?\S+\s+([^\s;|&]+)`)},
}

var (
	devNull = regexp.MustCompile(`/dev/(null|stdout|stderr|tty|fd)`)

	// A target must look like a path: it contains a separator, or it ends in
	// a short extension. This is what separates out.txt from %s.
	plausiblePath = regexp.MustCompile(`[/\\]|\.[A-Za-z0-9]{1,6}$`)

	// The gate for the python branch. Python appearing in a command proves
	// nothing — `yaml.safe_load(open(f))` opens a file and writes nothing.
	pythonWrites = regexp.MustCompile(`open\([^)]*,\s*['"][wa]|\.write\(|\.writelines\(|write_text\(|write_bytes\(|os\.replace\(|shutil\.(copy|move)`)

	// p = 'lit'  and  p = Path('lit') / pathlib.Path('lit'). Both bind a
	// literal path to a name and are the same fact; the second form was
	// missing from the original report's extractor and accounted for 70% of
	// the writes it could not name.
	pyStringVar = regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*)\s*=\s*['"]([^'"]+)['"]`)
	pyPathVar   = regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*)\s*=\s*(?:pathlib\.)?Path\(\s*['"]([^'"]+)['"]`)

	pyOpenVar          = regexp.MustCompile(`open\(\s*([A-Za-z_]\w*)\s*,\s*['"][wa]`)
	pyOpenLiteral      = regexp.MustCompile(`open\(\s*['"]([^'"]+)['"]\s*,\s*['"][wa]`)
	pyPathLiteralWrite = regexp.MustCompile(`(?:pathlib\.)?Path\(\s*['"]([^'"]+)['"]\s*\)\s*\.\s*write_(?:text|bytes)\(`)
	pyVarWrite         = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\.\s*write_(?:text|bytes)\(|\b([A-Za-z_]\w*)\s*\.\s*open\(\s*['"][wa]`)

	pyLiteralList   = regexp.MustCompile(`for\s+\w+\s+in\s*\[([^\]]+)\]`)
	pyStringLiteral = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

// pythonPathVars maps every variable holding a literal path, however it was
// bound. Only assignments in this same command are read: a variable set in
// an earlier command is not in scope here, and guessing across commands
// would invent a path rather than find one.
func pythonPathVars(command string) map[string]string {
	vars := map[string]string{}
	for _, m := range pyStringVar.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[2]
	}
	for _, m := range pyPathVar.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[2]
	}
	return vars
}
