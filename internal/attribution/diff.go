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
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var currentFile string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			currentFile = resolveDiffPath(line[4:], root)
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			if currentFile == "" {
				continue
			}
			content := line[1:]
			if !linehash.Substantive(content) {
				continue
			}
			hashes = append(hashes, linehash.Of(currentFile, content))
		}
	}
	return hashes
}

// resolveDiffPath turns a unified-diff "+++ b/some/path.go" target (or
// "/dev/null" for a deletion) into an absolute path under root.
func resolveDiffPath(target, root string) string {
	target = strings.TrimPrefix(target, "b/")
	if target == "/dev/null" {
		return ""
	}
	joined := filepath.Join(root, target)

	// Resolved so this side spells the path the way the agent side does.
	//
	// One filesystem location can have several names — /tmp against
	// /private/tmp on macOS, and on Windows the 8.3 short form
	// (C:\Users\RUNNER~1\…) against the long one (C:\Users\runneradmin\…).
	// The path is part of the line hash, so two spellings hash differently
	// and no staged line ever matches a recorded one: attribution degrades
	// to observed with nothing to indicate why.
	//
	// Done here, once per file in the diff, rather than inside linehash,
	// which runs per line and must not make a syscall.
	if resolved, err := filepath.EvalSymlinks(joined); err == nil {
		return resolved
	}
	return joined
}
