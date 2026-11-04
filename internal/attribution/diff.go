package attribution

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// StagedLines returns every substantive added line in the staged diff,
// hashed per line and scoped to its file (NAV-52).
//
// This is the unit that survives ordinary editing. Hashing whole tool
// outputs against whole diff hunks only matched when a file was created
// whole and committed untouched — 1 of 28 hunks on this project's own
// history. Per line, an agent's 200-line write still matches on the 150
// lines a developer kept.
//
// The returned slice may contain the same hash twice when a file legitimately
// repeats a line; callers that need a share should count distinct hashes.
func StagedLines() ([]uint64, error) {
	out, err := exec.Command("git", "diff", "--cached", "--unified=0").Output()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return stagedLineHashes(string(out), cwd), nil
}

// stagedLineHashes walks a unified diff and hashes each added line against
// the file it belongs to.
func stagedLineHashes(diff, root string) []uint64 {
	var hashes []uint64
	walkAddedLines(diff, root, func(_, abs, content string) {
		hashes = append(hashes, linehash.Of(abs, content))
	})
	return hashes
}

// walkAddedLines calls fn for every substantive added line of a unified
// diff, with the file's diff-relative path, its canonical absolute path
// under root, and the line's content.
//
// The one parser behind both the staged diff at commit time and committed
// history at sync time, so the two hash the same lines the same way.
func walkAddedLines(diff, root string, fn func(rel, abs, content string)) {
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var rel, abs string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			rel, abs = diffTarget(line[4:], root)
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			if abs == "" {
				continue
			}
			content := line[1:]
			if !linehash.Substantive(content) {
				continue
			}
			fn(rel, abs, content)
		}
	}
}

// diffTarget splits a "+++" target into its repo-relative path and its
// canonical absolute path. Both are empty for a deletion.
func diffTarget(target, root string) (rel, abs string) {
	rel = strings.TrimPrefix(target, "b/")
	if rel == "/dev/null" {
		return "", ""
	}
	return rel, resolveDiffPath(target, root)
}

// resolveDiffPath turns a unified-diff "+++ b/some/path.go" target (or
// "/dev/null" for a deletion) into an absolute path under root.
func resolveDiffPath(target, root string) string {
	target = strings.TrimPrefix(target, "b/")
	if target == "/dev/null" {
		return ""
	}
	// Canonicalised through the one function both sides of a match use.
	//
	// Once per file in the diff, not per line: linehash.Of runs for every
	// added line and must not make a syscall.
	return linehash.Canonical(filepath.Join(root, target))
}
