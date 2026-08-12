// Package purpose classifies a commit's purpose without a model, per NAV-43:
// Conventional Commits type first, then path heuristics, then diff shape.
// Target is 70-80% classified with zero model involvement; anything left
// over is Other rather than a guess.
package purpose

import (
	"regexp"
	"strings"
)

type Purpose string

const (
	Feature    Purpose = "feature"
	Fix        Purpose = "fix"
	Test       Purpose = "test"
	Refactor   Purpose = "refactor"
	Docs       Purpose = "docs"
	Config     Purpose = "config"
	Chore      Purpose = "chore"
	Migration  Purpose = "migration"
	Dependency Purpose = "dependency"
	Other      Purpose = "other"
)

// conventionalTypes maps a Conventional Commits type prefix to a Purpose.
// "ci" and "build" fold into Config — both are pipeline/tooling config, not
// worth a separate bucket at this granularity.
var conventionalTypes = map[string]Purpose{
	"feat":     Feature,
	"fix":      Fix,
	"test":     Test,
	"refactor": Refactor,
	"docs":     Docs,
	"chore":    Chore,
	"ci":       Config,
	"build":    Config,
	"style":    Refactor,
	"perf":     Refactor,
}

var conventionalPrefix = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?!?:\s`)

// Classify determines a commit's purpose from its message and changed files.
// Order: Conventional Commits type (highest confidence) -> path heuristics
// -> diff shape. Falls through to Other rather than guessing.
func Classify(commitMsg string, files []string) Purpose {
	if p, ok := fromConventionalCommit(commitMsg); ok {
		return p
	}
	if p, ok := fromPaths(files); ok {
		return p
	}
	if p, ok := fromDiffShape(files); ok {
		return p
	}
	return Other
}

func fromConventionalCommit(msg string) (Purpose, bool) {
	firstLine := strings.SplitN(msg, "\n", 2)[0]
	m := conventionalPrefix.FindStringSubmatch(firstLine)
	if m == nil {
		return "", false
	}
	p, ok := conventionalTypes[m[1]]
	return p, ok
}

// pathClassifiers is tried in order; the first one every changed file
// matches wins. Order matters: dependency manifests and migrations are
// narrower, stronger signals than the general test/docs/config patterns.
var pathClassifiers = []struct {
	purpose Purpose
	match   func(lowerPath string) bool
}{
	{Dependency, isDependencyManifest},
	{Migration, func(p string) bool { return strings.Contains(p, "migration") }},
	{Test, isTestPath},
	{Docs, isDocsPath},
	{Config, isConfigPath},
}

// fromPaths classifies by directory/filename convention when no Conventional
// Commits prefix was present. Every changed file must match the same
// category — a mixed change (e.g. a source file plus its test) falls
// through rather than being misclassified by whichever pattern happened
// first.
func fromPaths(files []string) (Purpose, bool) {
	if len(files) == 0 {
		return "", false
	}

	for _, c := range pathClassifiers {
		allMatch := true
		for _, f := range files {
			if !c.match(strings.ToLower(f)) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return c.purpose, true
		}
	}
	return "", false
}

func isDependencyManifest(path string) bool {
	base := path[strings.LastIndex(path, "/")+1:]
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "yarn.lock",
		"pnpm-lock.yaml", "cargo.toml", "cargo.lock", "gemfile", "gemfile.lock",
		"requirements.txt", "poetry.lock", "pyproject.toml":
		return true
	}
	return false
}

func isTestPath(path string) bool {
	return strings.Contains(path, "_test.go") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, ".test.") ||
		strings.HasSuffix(path, "_spec.rb")
}

func isDocsPath(path string) bool {
	return strings.HasSuffix(path, ".md") ||
		strings.HasPrefix(path, "docs/") ||
		strings.Contains(path, "/docs/") ||
		strings.HasSuffix(path, "readme")
}

func isConfigPath(path string) bool {
	return strings.HasPrefix(path, ".github/") ||
		strings.HasSuffix(path, ".yml") ||
		strings.HasSuffix(path, ".yaml") ||
		strings.HasSuffix(path, ".toml") ||
		strings.Contains(path, "dockerfile") ||
		strings.HasSuffix(path, ".gitignore")
}

// fromDiffShape is the last deterministic step: a change touching only one
// or two files with no other signal is more likely a focused fix than a
// feature; broad multi-file changes lean refactor. Weak signal, used only
// when nothing else matched.
func fromDiffShape(files []string) (Purpose, bool) {
	switch {
	case len(files) == 0:
		return "", false
	case len(files) >= 6:
		return Refactor, true
	default:
		return "", false
	}
}
