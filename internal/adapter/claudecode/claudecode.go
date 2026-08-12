// Package claudecode adapts Claude Code's local session transcripts
// (~/.claude/projects/<slug>/<session>.jsonl) into journal.Entry records.
//
// Only tool_use records for Edit/Write are read. Prompt text, other message
// content, and any field not needed for attribution are ignored entirely —
// the journal's no-prompt-text constraint is enforced by never extracting it.
package claudecode

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

const AgentName = "claude-code"

// ProjectsDir returns the root directory Claude Code stores session
// transcripts under, honoring CLAUDE_CONFIG_DIR like the CLI itself does.
func ProjectsDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// SlugForCwd reproduces Claude Code's directory-name encoding: every '/' in
// the absolute path becomes '-'.
func SlugForCwd(cwd string) string {
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}

// SessionDir returns the directory Claude Code stores this repo's session
// transcripts in.
func SessionDir(cwd string) string {
	return filepath.Join(ProjectsDir(), SlugForCwd(cwd))
}

// SessionFiles returns every .jsonl transcript path for the given repo cwd.
func SessionFiles(cwd string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(SessionDir(cwd), "*.jsonl"))
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// record is the subset of a transcript line this adapter needs.
type record struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"sessionId"`
	Version   string    `json:"version"`
	Message   struct {
		Content []toolUseBlock `json:"content"`
	} `json:"message"`
}

type toolUseBlock struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath  string `json:"file_path"`
		Content   string `json:"content"`    // Write
		OldString string `json:"old_string"` // Edit
		NewString string `json:"new_string"` // Edit
	} `json:"input"`
}

// ParseSince reads every tool_use (Edit/Write) event at or after `since`
// from the given transcript file. Unrecognized lines are skipped, never
// fatal — a malformed or future transcript format degrades to fewer
// entries rather than a hard failure (fail to undetermined, not error).
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []journal.Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Type != "assistant" || r.Timestamp.Before(since) {
			continue
		}

		for _, block := range r.Message.Content {
			if block.Type != "tool_use" {
				continue
			}
			if block.Name != "Edit" && block.Name != "Write" {
				continue
			}

			added, removed := diffStat(block.Name, block.Input.Content, block.Input.OldString, block.Input.NewString)

			// Hash each line the agent produced, not just the whole output
			// (NAV-52). A Write's content or an Edit's replacement text is
			// what ends up in the file, so those lines are what a staged
			// diff can be matched against line by line.
			produced := block.Input.NewString
			if block.Name == "Write" {
				produced = block.Input.Content
			}

			entries = append(entries, journal.Entry{
				Timestamp:    r.Timestamp,
				Agent:        AgentName,
				AgentVersion: r.Version,
				Session:      r.SessionID,
				Event:        "tool_use",
				Tool:         block.Name,
				File:         block.Input.FilePath,
				LinesAdded:   added,
				LinesRemoved: removed,
				HunkHash:     hunkHash(block.Input.FilePath, block.Name, block.Input.Content, block.Input.NewString),
				LineHashes:   linehash.OfText(block.Input.FilePath, produced),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func diffStat(tool, content, oldString, newString string) (added, removed int) {
	if tool == "Write" {
		return countLines(content), 0
	}
	return countLines(newString), countLines(oldString)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// hunkHash identifies a change by its file plus the resulting content it
// introduces, not by commit sha — a commit may not exist yet when the
// observation is recorded, and may later be amended, rebased, or squashed
// (NAV-26: match by content hash, never sha). Keying on (file, resultText)
// rather than resultText alone avoids a false intersected match when the
// same small fragment (e.g. a common one-line import) is independently
// written to two different files — this is what lets the hash stay
// comparable to a staged git diff's per-file added lines later.
func hunkHash(filePath, tool, content, resultText string) string {
	text := resultText
	if tool == "Write" {
		text = content
	}
	sum := sha256.Sum256([]byte(filePath + "\x00" + text))
	return "sha256:" + hex.EncodeToString(sum[:])
}
