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

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

const AgentName = "claude-code"

// ProjectsDir returns the root directory Claude Code stores session
// transcripts under.
//
// Honors CLAUDE_CONFIG_DIR like the CLI itself does, and lets a whodunit
// override win over both that and the built-in default — the escape hatch
// for a machine where the convention is wrong (NAV-71). Windows is the
// known case: the directory-name encoding below is unverified there.
func ProjectsDir() string {
	path, _ := adapter.ResolveRoot(AgentName, builtinProjectsDir())
	return path
}

// builtinProjectsDir is the location Claude Code uses when nothing
// overrides it. Kept separate so the override chain has a default to fall
// back to rather than recomputing it.
func builtinProjectsDir() string {
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
	ID    string `json:"id"`          // on tool_use blocks
	UseID string `json:"tool_use_id"` // on tool_result blocks, links back to the call
	Error bool   `json:"is_error"`
	Input struct {
		FilePath  string `json:"file_path"`
		Content   string `json:"content"`    // Write
		OldString string `json:"old_string"` // Edit
		NewString string `json:"new_string"` // Edit
	} `json:"input"`

	// Content of a tool_result, which Claude Code writes either as a plain
	// string or as an array of blocks depending on the tool.
	Result resultContent `json:"content"`
}

// resultContent accepts both shapes a tool_result's content can take.
type resultContent struct{ Text string }

func (r *resultContent) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r.Text = s
		return nil
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
			sb.WriteByte(' ')
		}
		r.Text = sb.String()
		return nil
	}
	// An unrecognised shape is not an error: outcome degrades to unknown
	// rather than failing the whole transcript.
	return nil
}

// ParseSince reads every tool_use (Edit/Write) event at or after `since`
// from the given transcript file, along with what happened to each call.
//
// Two passes: a tool call and its result are separate records, and the
// result arrives later in the file, so outcomes are collected first and
// then joined by tool_use_id. One pass would mean guessing at ordering.
//
// Unrecognized lines are skipped, never fatal — a malformed or future
// transcript format degrades to fewer entries rather than a hard failure
// (fail to undetermined, not error).
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	// A transcript untouched since the cutoff has nothing to contribute, so
	// it is not opened at all.
	//
	// This is the common case, not an optimisation for a rare one: the
	// daemon re-runs ingest on a timer and the hook runs on every commit,
	// both over every transcript on the machine. Without this, collecting
	// outcomes below reads the whole file before the cutoff is ever
	// consulted — making a no-op ingest cost *more* than a real one, since
	// it pays for the outcome pass and then discards every event.
	if !since.IsZero() {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(since) {
			return nil, nil
		}
	}

	outcomes, err := collectOutcomes(path)
	if err != nil {
		return nil, err
	}

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
				// Every other tool is recorded as a bare call: which tool,
				// when, in which session. No file, no line hashes, no
				// arguments.
				//
				// The arguments are the reason this is a separate path
				// rather than a relaxed filter. A Bash command line, a Read
				// path, an MCP payload — any of them can carry file
				// contents or prompt text, which this package does not
				// collect by construction (NAV-25). Taking the name and
				// dropping block.Input keeps that property.
				entries = append(entries, journal.Entry{
					Timestamp:    r.Timestamp,
					Agent:        AgentName,
					AgentVersion: r.Version,
					Session:      r.SessionID,
					Event:        "tool_call",
					Tool:         block.Name,
				})
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

			outcome, ok := outcomes[block.ID]
			if !ok {
				// No result found: the transcript may be truncated, or the
				// session still running. Recorded as unknown rather than
				// assumed accepted, which would flatter the rate.
				outcome = OutcomeUnknown
			}

			// A rejected or failed call never reached the file, so its text
			// must not count as agent-authored code. Carrying line hashes
			// for it would attribute lines that do not exist.
			lineHashes := linehash.OfText(block.Input.FilePath, produced)
			if outcome != OutcomeAccepted {
				lineHashes = nil
				added, removed = 0, 0
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
				LineHashes:   lineHashes,
				Outcome:      string(outcome),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ParseSessionActivity summarises engagement per session in a transcript:
// how much conversation and tool use it contained (NAV-55).
//
// Counts only. No message text, no tool arguments, nothing derived from
// what was written — a message count needs no message content, which is
// what makes this compatible with the no-prompt-text rule.
//
// Returns journal.Session directly rather than an adapter-specific type.
// An earlier SessionActivity struct duplicated it field for field, which
// bought nothing and cost a copy loop at every call site.
func ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Named rather than embedded: journal.Session has a field also called
	// Session, and embedding makes `a.Session` ambiguous between the two.
	type acc struct {
		s     journal.Session
		tools map[string]bool
	}
	sessions := map[string]*acc{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.SessionID == "" || r.Timestamp.Before(since) {
			continue
		}

		a, ok := sessions[r.SessionID]
		if !ok {
			a = &acc{tools: map[string]bool{}}
			a.s.Session = r.SessionID
			a.s.Agent = AgentName
			a.s.FirstSeen = r.Timestamp
			sessions[r.SessionID] = a
		}
		if r.Version != "" {
			a.s.AgentVersion = r.Version
		}
		if r.Timestamp.After(a.s.LastSeen) {
			a.s.LastSeen = r.Timestamp
		}

		switch r.Type {
		case "user":
			// A user record carrying only tool results is the harness
			// replying to the agent, not a person typing.
			if !hasToolResult(r) {
				a.s.UserMessages++
			}
		case "assistant":
			a.s.AgentMessages++
		}

		for _, block := range r.Message.Content {
			if block.Type != "tool_use" {
				continue
			}
			a.s.ToolCalls++
			a.tools[block.Name] = true
			if strings.HasPrefix(block.Name, "mcp__") {
				a.s.MCPCalls++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	out := make([]journal.Session, 0, len(sessions))
	for _, a := range sessions {
		a.s.DistinctTools = len(a.tools)
		out = append(out, a.s)
	}
	return out, nil
}

func hasToolResult(r record) bool {
	for _, b := range r.Message.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// collectOutcomes maps each tool call's id to what happened to it, from the
// tool_result blocks scattered through the transcript.
func collectOutcomes(path string) (map[string]Outcome, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	outcomes := map[string]Outcome{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		for _, block := range r.Message.Content {
			if block.Type != "tool_result" || block.UseID == "" {
				continue
			}
			outcomes[block.UseID] = classifyResult(block.Error, block.Result.Text)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return outcomes, nil
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
