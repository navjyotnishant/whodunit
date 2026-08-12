package attribution

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StagedHunkHashes returns the hunks present in the current staged diff,
// keyed the same way claudecode.hunkHash keys a journal entry: sha256 of
// (absolute file path, added text a single hunk introduces). This is what
// lets Determine promote a match from method=observed (same file touched)
// to method=intersected (the exact text the agent wrote is what got
// staged) without a false match when two different files happen to gain
// identical fragments.
//
// The value is the hunk's line count, so a caller can measure the agent's
// share of the commit in STAGED lines. Counting journal lines instead
// would count every rewrite of the same block: on this project's own
// history that inflated the numerator more than fourfold, because an agent
// writes a file, rewrites it, and rewrites it again while the commit holds
// only the final state.
func StagedHunkHashes() (map[string]int, error) {
	out, err := exec.Command("git", "diff", "--cached", "--unified=0").Output()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return hashAddedHunks(string(out), cwd)
}

// hashAddedHunks walks a unified diff and hashes each hunk's contiguous
// block of added ('+') lines together with the file it belongs to
// (resolved against root, since journal entries store absolute paths). A
// hunk with mixed add/remove lines separated by context still produces one
// hash per contiguous added block, matching how claudecode hashes one
// edit's resulting text as a single unit.
//
// Each hash maps to the number of lines that hunk contributes.
func hashAddedHunks(diff, root string) (map[string]int, error) {
	hashes := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var currentFile string
	var added []string
	flush := func() {
		if len(added) == 0 {
			return
		}
		sum := sha256.Sum256([]byte(currentFile + "\x00" + strings.Join(added, "\n")))
		hashes["sha256:"+hex.EncodeToString(sum[:])] = len(added)
		added = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			flush()
			currentFile = resolveDiffPath(line[4:], root)
		case strings.HasPrefix(line, "--- "):
			continue
		case strings.HasPrefix(line, "@@"):
			flush()
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		default:
			flush()
		}
	}
	flush()

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return hashes, nil
}

// resolveDiffPath turns a unified-diff "+++ b/some/path.go" target (or
// "/dev/null" for a deletion) into an absolute path under root.
func resolveDiffPath(target, root string) string {
	target = strings.TrimPrefix(target, "b/")
	if target == "/dev/null" {
		return ""
	}
	return filepath.Join(root, target)
}
