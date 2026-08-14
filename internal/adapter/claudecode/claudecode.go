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

// SlugForCwd reproduces Claude Code's directory-name encoding: every path
// separator in the absolute path becomes '-'.
//
// Both separators are replaced regardless of platform. A path can carry
// forward slashes on Windows — Go's own APIs accept them, and MSYS or WSL
// hand them over routinely — so keying off filepath.Separator alone would
// encode the same directory two different ways depending on who produced the
// string.
//
// The colon is dropped because Windows will not accept it in a filename. A
// slug of "C:-Users-me-repo" cannot be created at all: every mkdir fails
// with "The directory name is invalid", so no transcript is ever found and
// every commit lands undetermined — the silent failure NAV-21 exists to
// prevent, wearing the mask of "no AI was used".
//
// Dropped rather than mapped to '-' so C:\repo and C-\repo cannot collide.
//
// CAVEAT: this makes the slug legal on Windows. It does NOT establish that
// it matches what Claude Code itself writes there — that needs a Windows
// machine with the client installed, and is still open (NAV-81,
// docs/adapters/agent-support.md). If the encodings differ, the adapter
// finds no transcripts on Windows. It fails the same way it does today,
// so this is strictly an improvement, but it is not yet Windows support.
func SlugForCwd(cwd string) string {
	slug := strings.NewReplacer(
		"/", "-",
		`\`, "-",
	).Replace(cwd)
	return strings.ReplaceAll(slug, ":", "")
}

// SessionDir returns the directory Claude Code stores this repo's session
// transcripts in.
func SessionDir(cwd string) string {
	// Resolved first, because Claude Code encodes the directory it actually
	// resolved to and the hook is handed whatever path the shell was in.
	//
	// One location, several names: /tmp against /private/tmp on macOS, and
	// on Windows the 8.3 short form (C:\Users\RUNNER~1\…) against the long
	// one. Slugging the unresolved spelling produces a directory name that
	// exists nowhere, so no transcript is found, and every commit is stamped
	// undetermined — reading as "no AI was used" rather than "the adapter
	// looked in the wrong place" (NAV-21).
	return filepath.Join(ProjectsDir(), SlugForCwd(linehash.Canonical(cwd)))
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

	// Both sit at the top level of the record rather than on the message,
	// and permissionMode is written on USER records rather than assistant
	// ones — measured, not assumed. Reading them off the assistant turn
	// finds nothing.
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`

	// The branch the work landed on. 112 distinct values across the
	// corpus on this machine, of which "HEAD" is 8% — a detached head,
	// which is a real state rather than a missing value, so it is stored
	// as written rather than blanked.
	GitBranch string `json:"gitBranch"`

	// Which MCP server and method a call went through. Claude Code
	// resolves these itself, so no parsing of the mcp__server__method
	// name is needed — 6 servers and 64 methods observed.
	MCPServer string `json:"attributionMcpServer"`
	MCPTool   string `json:"attributionMcpTool"`

	// ToolUseResult is a sibling of message, not part of it, and carries
	// stdout, stderr, file bodies and diffs — all forbidden (NAV-25).
	// Only the one scalar is lifted out; the rest is never unmarshalled,
	// which is what keeps the constraint structural rather than a rule
	// someone has to remember.
	ToolUseResult struct {
		UserModified *bool `json:"userModified"`
	} `json:"toolUseResult"`
	Message struct {
		Content []toolUseBlock `json:"content"`

		// ID identifies the assistant message, which is NOT one-to-one
		// with a record: one message is written as several lines, one per
		// content block, each repeating the same usage. Deduplicating on
		// it is what stops token counts being multiplied — see
		// readUsage.
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

// modelOf returns the record's model, excluding the synthetic sender.
func modelOf(r record) string {
	if r.Message.Model == syntheticModel {
		return ""
	}
	return r.Message.Model
}

// userModifiedOf returns a pointer only when the transcript actually said
// something. A missing entry is "this call has no edit signal", which must
// stay nil rather than becoming false — false is a claim that nobody
// edited it (NAV-21).
func userModifiedOf(edited map[string]bool, id string) *bool {
	v, ok := edited[id]
	if !ok {
		return nil
	}
	return &v
}

// syntheticModel is the sender on records Claude Code generates itself
// rather than receiving from a model — 5 of 161 assistant records in a
// recent sample, all carrying zero output tokens.
//
// Excluded from model attribution because it is not a model: left in, it
// becomes a row on every per-model panel with no tokens against it, and
// in any ratio-against-baseline it is the cheapest series, making every
// comparison against it infinite.
const syntheticModel = "<synthetic>"

// usage is Claude Code's per-turn token report. Present on 100% of
// assistant turns — not sampled, not optional.
//
// Per turn, unlike Codex's cumulative totals, so these are summed rather
// than overwritten. Getting that backwards in either direction is the
// expensive mistake in this area.
type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
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

	outcomes, edited, err := collectOutcomesAndEdits(path)
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
					Model:        modelOf(r),
					Branch:       r.GitBranch,
					MCPServer:    r.MCPServer,
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
			lineHashes := linehash.OfText(linehash.Canonical(block.Input.FilePath), produced)
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
				Model:        modelOf(r),
				Branch:       r.GitBranch,
				MCPServer:    r.MCPServer,
				UserModified: userModifiedOf(edited, block.ID),
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

		// Assistant message ids already counted. One message is written as
		// several records — one per content block — each repeating the
		// same id, model and usage. Measured on the largest transcript on
		// this machine: 12,029 usage-bearing records for 6,687 distinct
		// messages, so summing per record inflates every token count by
		// 1.93x.
		seenMessages map[string]bool

		// Token totals, accumulated separately because journal.Session
		// carries them as pointers — nil there means "this agent does not
		// report it", and Claude Code always does, so the distinction is
		// made once at the end rather than on every addition.
		tokens    usage
		anyTokens bool
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
			a = &acc{tools: map[string]bool{}, seenMessages: map[string]bool{}}
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
			// Counted once per MESSAGE, not once per record.
			//
			// One assistant message is written as several records — one
			// per content block — each repeating the same id, model and
			// usage. On the largest transcript on this machine that is
			// 12,029 records for 6,687 messages.
			//
			// This corrects AgentMessages as well as the token counts.
			// Both were inflated by 1.93x here, and Codex was already
			// counting messages rather than records, so the same column
			// meant different things depending on which agent filled it —
			// which makes any cross-agent comparison of engagement wrong
			// in a way nothing on the dashboard reveals.
			//
			// A record with no message id is counted: it cannot be
			// deduplicated, and dropping it would undercount instead.
			if id := r.Message.ID; id != "" {
				if a.seenMessages[id] {
					break
				}
				a.seenMessages[id] = true
			}
			a.s.AgentMessages++
			if u := r.Message.Usage; u != nil {
				a.tokens.InputTokens += u.InputTokens
				a.tokens.OutputTokens += u.OutputTokens
				a.tokens.CacheReadInputTokens += u.CacheReadInputTokens
				a.tokens.CacheCreationInputTokens += u.CacheCreationInputTokens
				a.anyTokens = true
			}
			// The last model seen wins. A session can change model
			// part-way through, and the turn that finished the work is the
			// one worth attributing.
			if r.Message.Model != "" && r.Message.Model != syntheticModel {
				a.s.Model = r.Message.Model
			}
		}

		// Outside the switch: permissionMode is recorded on user records,
		// and effort can appear on either. Reading them per record rather
		// than per message is right — they describe the turn's settings,
		// not the message, and the last one seen is the one in force.
		if r.Effort != "" {
			a.s.Effort = r.Effort
		}
		if r.PermissionMode != "" {
			a.s.PermissionMode = r.PermissionMode
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

		// Assigned only when at least one turn reported usage, so a
		// transcript that carries none leaves nil rather than a row of
		// zeroes. Claude Code reports usage on every assistant turn, so in
		// practice this is always taken — but a session with no assistant
		// turns at all (a transcript that opens and is abandoned) must not
		// claim it cost nothing (NAV-21).
		if a.anyTokens {
			a.s.InputTokens = int64p(a.tokens.InputTokens)
			a.s.OutputTokens = int64p(a.tokens.OutputTokens)
			a.s.CacheReadTokens = int64p(a.tokens.CacheReadInputTokens)
			a.s.CacheWriteTokens = int64p(a.tokens.CacheCreationInputTokens)
		}

		// Deliberately not set: Claude Code records no per-turn timing and
		// does not separate reasoning tokens. Left nil rather than zero —
		// "not reported" is not "instantaneous", and a latency panel
		// averaging in zeroes would report this agent as the fastest.

		out = append(out, a.s)
	}
	return out, nil
}

func int64p(v int64) *int64 { return &v }

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
// collectOutcomes maps each tool call's id to what happened to it, and to
// whether a human edited the result.
//
// userModified rides along here rather than in a third pass because it is
// recorded in the same place: on the USER record that carries the
// tool_result, keyed by the same tool_use_id. It is the one signal that
// separates "the agent wrote this" from "the agent wrote this and it was
// kept" — and only Claude Code has it.
//
// CAVEAT: observed 7,201 times on this machine and false every single
// time. The field is read correctly and the true case is unverified.
func collectOutcomesAndEdits(path string) (map[string]Outcome, map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	outcomes := map[string]Outcome{}
	edited := map[string]bool{}
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
			if v := r.ToolUseResult.UserModified; v != nil {
				edited[block.UseID] = *v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return outcomes, edited, nil
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
