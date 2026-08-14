// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Parses Codex's apply_patch format and classifies outcomes.

package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

// FilePatch is one file's worth of change within an apply_patch call.
type FilePatch struct {
	Path    string
	Op      string // Add | Update | Delete
	Added   int
	Removed int

	// AddedLines is the text the agent produced, which is what line
	// hashing needs. Removed lines are counted but not kept: they are
	// existing code, not the agent's output.
	AddedLines []string
}

// parsePatch reads Codex's apply_patch envelope:
//
//	*** Begin Patch
//	*** Update File: path/to/file
//	@@
//	-old line
//	+new line
//	*** End Patch
//
// A malformed patch yields the files it could read rather than an error —
// partial evidence beats discarding a whole session (NAV-21).
func parsePatch(input string) []FilePatch {
	var out []FilePatch
	var cur *FilePatch

	flush := func() {
		if cur != nil && cur.Path != "" {
			out = append(out, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(input, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "),
			strings.HasPrefix(line, "*** Update File: "),
			strings.HasPrefix(line, "*** Delete File: "):
			flush()
			op, path := splitFileHeader(line)
			cur = &FilePatch{Path: path, Op: op}

		case strings.HasPrefix(line, "*** End Patch"):
			flush()

		case cur == nil:
			// Text before the first file header (including "*** Begin
			// Patch") belongs to no file.

		case strings.HasPrefix(line, "+"):
			cur.Added++
			cur.AddedLines = append(cur.AddedLines, strings.TrimPrefix(line, "+"))

		case strings.HasPrefix(line, "-"):
			cur.Removed++
		}
	}
	flush()
	return out
}

func splitFileHeader(line string) (op, path string) {
	rest := strings.TrimPrefix(line, "*** ")
	op, path, _ = strings.Cut(rest, " File: ")
	return op, strings.TrimSpace(path)
}

// Outcome is what happened to a tool call (NAV-54).
type Outcome string

const (
	OutcomeAccepted Outcome = "accepted"
	OutcomeRejected Outcome = "rejected"
	OutcomeFailed   Outcome = "failed"
	OutcomeUnknown  Outcome = "unknown"
)

// Codex reports an applied patch by listing the files it touched. Anything
// else is a patch that did not land.
const successMarker = "Success. Updated the following files:"

// rejectionMarkers are the phrases Codex uses when a human declined the
// call, as opposed to the tool failing on its own.
//
// Matching on prose is fragile, and the failure mode is silent and
// flattering — every rejection quietly becoming an acceptance. The golden
// fixtures (NAV-30) exist to fail loudly when these stop matching.
var rejectionMarkers = []string{
	"rejected by the user",
	"user rejected",
	"user declined",
	"aborted by user",
}

// classify decides what an apply_patch output means.
func classify(output string) Outcome {
	if output == "" {
		return OutcomeUnknown
	}
	lower := strings.ToLower(output)
	for _, m := range rejectionMarkers {
		if strings.Contains(lower, m) {
			return OutcomeRejected
		}
	}
	if strings.Contains(output, successMarker) {
		return OutcomeAccepted
	}
	// Everything else is a patch that did not apply — most commonly
	// "apply_patch verification failed: Failed to find expected lines".
	return OutcomeFailed
}

// collectOutcomes maps call_id to what happened, in one pass over the file.
//
// Codex writes the output either as a plain string or as a JSON object with
// its own "output" key, depending on version, so both are accepted rather
// than assuming the shape this machine happens to produce.
func collectOutcomes(path string) (map[string]Outcome, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]Outcome{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Type != "response_item" {
			continue
		}
		var o toolOutput
		if err := json.Unmarshal(r.Payload, &o); err != nil {
			continue
		}
		if o.Type != "custom_tool_call_output" || o.CallID == "" {
			continue
		}
		out[o.CallID] = classify(unwrapOutput(o.Output))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// unwrapOutput returns the human-readable text of a tool output, whether
// Codex wrote it plainly or wrapped in a JSON object.
func unwrapOutput(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return s
	}
	var wrapped struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapped); err == nil && wrapped.Output != "" {
		return wrapped.Output
	}
	return s
}
