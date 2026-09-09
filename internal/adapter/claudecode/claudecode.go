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
	"regexp"
	"strconv"
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

// SlugForCwd reproduces Claude Code's directory-name encoding: every
// character that is not a letter or a digit becomes '-'.
//
// This mirrors the client's own rule, which is a single character class
// rather than a list of separators:
//
//	path.replace(/[^a-zA-Z0-9]/g, "-")
//
// Matching the whole class matters, and getting it narrower is a silent
// failure rather than a partial one. This function previously replaced only
// '/' and '\\' and DROPPED ':', which meant every path containing any other
// punctuation resolved to a directory that does not exist:
//
//	~/.orion/…  ->  Claude Code writes  -Users-me--orion-…
//	                whodunit looked for -Users-me-.orion-…
//
// A dotted parent directory is the common case — every agent sandbox under
// ~/.orion, and any ~/.config-style layout. Measured on one machine: 91 git
// repositories, 14 found under the old encoding, 72 under this one. The 77
// misses were not reported as misses. The adapter found no transcript,
// attribution concluded no agent had touched the repository, and commits an
// agent wrote end to end were stamped unassisted — a positive claim that no
// AI was involved, which is the exact shape NAV-21 exists to prevent.
//
// The colon is now mapped rather than dropped. Dropping it was deliberate,
// to keep C:\repo and C-\repo apart, but it did not match the client: Claude
// Code writes C--repo for the former. Correctness against the thing we are
// trying to find beats a collision that upstream itself does not avoid
// (NAV-81).
//
// A slug longer than maxSlugLen is truncated and given a hash of the FULL
// path, so two long paths sharing a 200-character prefix stay apart. That
// mirrors the client:
//
//	if (slug.length <= 200) return slug;
//	return `${slug.slice(0, 200)}-${hash(path).toString(36)}`;
//
// Note the hash is over the ORIGINAL path, not over the slug — hashing the
// slug would collide exactly where the truncation needs to disambiguate,
// since the encoding is lossy.
//
// Verified by experiment rather than by reading the client: a directory was
// created with a >200-character path, Claude Code was run in it, and the
// name it wrote was compared against this function. Two shapes, both exact —
// a 241-character slug of plain segments, and a 202-character one whose
// segments carry underscores and dots (WHO-237).
func SlugForCwd(cwd string) string {
	slug := nonAlnum.ReplaceAllString(cwd, "-")
	if len(slug) <= maxSlugLen {
		return slug
	}
	return slug[:maxSlugLen] + "-" + strconv.FormatUint(uint64(pathHash(cwd)), 36)
}

// maxSlugLen is where Claude Code truncates. A literal, because it is
// upstream's number rather than a choice available to us.
const maxSlugLen = 200

// nonAlnum is every character Claude Code replaces with '-'. Compiled once:
// SlugForCwd runs on the commit path, where the hook's whole budget is a
// fraction of a second.
var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// pathHash is the classic h = h*31 + c string hash over the path, truncated
// to 32 bits — what the client uses, confirmed by matching the directory
// names it actually wrote.
//
// Iterates RUNES, not bytes. The client hashes with charCodeAt, which yields
// UTF-16 code units, so a byte-wise loop diverges on any non-ASCII path: for
// /Users/nav/café/repo the client produces `vhp8hq` and a byte loop produces
// `1d1uy2d`, and the derived directory name then exists nowhere. Only a slug
// over maxSlugLen can carry the hash at all — the prefix is ASCII by
// construction, since the regex has already replaced every non-alphanumeric
// character — but that is precisely the silent miss this function exists to
// prevent.
//
// Exact for the BMP, which is every realistic path. A character outside it —
// an emoji in a directory name — is one rune here and a surrogate PAIR in
// UTF-16, so the client would fold two units where this folds one. Left
// as-is rather than hand-rolling surrogate encoding for a case no repository
// has: the failure stays a missed transcript, never a wrong attribution.
//
// uint32 rather than int32 is NOT settled. JavaScript's bitwise ops yield a
// signed value and toString(36) renders a negative one with a leading '-',
// which would make roughly half of all long slugs differ from this. Both
// fixtures in the tests hash positive, so the branch has never been
// exercised, and the one pre-existing >200-character directory on this
// machine cannot settle it either — its 200-character prefix already ends in
// '-', so both readings produce the same name. Resolving it needs one more
// observed directory whose path is known to hash negative (WHO-237).
func pathHash(path string) uint32 {
	var h uint32
	for _, r := range path {
		h = h*31 + uint32(r)
	}
	return h
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

	// Subtype distinguishes the system records. compact_boundary is the
	// one this reads (NAV-106).
	Subtype string `json:"subtype"`

	// CompactMetadata rides on a compact_boundary record and carries far
	// more than is taken from it: preCompactDiscoveredTools lists every
	// tool name in the session, and slug is a human-readable label derived
	// from the conversation. Only the trigger and the two token counts are
	// declared here, so the rest is never unmarshalled at all (NAV-25).
	CompactMetadata *struct {
		// "auto" when the agent compacted because it had to, "manual"
		// when a human ran /compact. The distinction is the whole point:
		// an auto compaction is the tool coping, a manual one is someone
		// managing their context deliberately.
		Trigger    string `json:"trigger"`
		PreTokens  int64  `json:"preTokens"`
		PostTokens int64  `json:"postTokens"`
	} `json:"compactMetadata"`

	// The working directory the command ran in. Needed to resolve a
	// relative write target — "cat > x.go" names a file only when you know
	// where it ran (WHO-238).
	Cwd string `json:"cwd"`

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

		// Command is read for Bash and never recorded. WriteTargets takes
		// the file names out of it and the string is discarded with the
		// block — a heredoc body is file content and does not belong in the
		// journal (NAV-25, WHO-238). Asserted by entryfields_test.go.
		Command string `json:"command"` // Bash
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

				// A Bash command that WRITES a file is agent authorship and
				// has to be attributable, or an agent that edits through a
				// shell is invisible while one that uses Edit is not. On this
				// machine that gap was 7,263 Bash calls against 894 Edit and
				// Write ones (WHO-238).
				//
				// Emitted ALONGSIDE the tool_call above, not instead of it:
				// the call is still a call, and everything already correlating
				// on tool_call keeps working unchanged.
				//
				// Only the file NAMES leave this block. block.Input.Command is
				// read here and goes out of scope with the loop iteration —
				// the heredoc body in `cat > f <<'EOF' … EOF` is file content
				// and never reaches an entry (NAV-25).
				if block.Name == "Bash" {
					targets, incomplete := WriteTargets(block.Input.Command, r.Cwd)
					for _, target := range targets {
						// No LinesAdded and no HunkHash, deliberately. The
						// command carries no diff, and a fabricated one would
						// let a Bash edit claim `intersected` on evidence that
						// does not exist. Absent beats invented (NAV-21).
						e := journal.Entry{
							Timestamp:    r.Timestamp,
							Agent:        AgentName,
							AgentVersion: r.Version,
							Session:      r.SessionID,
							Event:        "tool_use",
							Tool:         block.Name,
							File:         target,
							Model:        modelOf(r),
							Branch:       r.GitBranch,
							MCPServer:    r.MCPServer,
						}
						// A command that also writes somewhere this could not
						// name yields an INCOMPLETE file set. Recording it as
						// ordinary evidence would let a partial list read as a
						// full one, which is the wrong answer wearing the shape
						// of a finding.
						if incomplete {
							e.Outcome = string(OutcomePartial)
						}
						entries = append(entries, e)
					}
				}
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

		// Compactions counted for this session, and how many were manual
		// rather than forced.
		compactions int64
		manual      int64

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

		// A compact boundary is a system record, not a message, so it is
		// handled before the message switch rather than inside it.
		if r.Type == "system" && r.Subtype == "compact_boundary" {
			a.compactions++
			if m := r.CompactMetadata; m != nil && m.Trigger == "manual" {
				a.manual++
			}
			continue
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
		// Always assigned, including zero — and that is the right call
		// here, unlike every other nullable field on this struct.
		//
		// The distinction NAV-21 protects is "measured as nothing" versus
		// "could not measure". For tokens the second is real: an agent
		// that does not report usage leaves nothing to read. For
		// compactions it is not: the transcript parsed, every record was
		// examined, and no boundary was present. That IS zero.
		//
		// Reporting nil instead would make the compact rate uncomputable,
		// because the denominator — sessions that could have compacted —
		// would be empty. Measured on 400 transcripts here, 2 compacted;
		// the other 398 are zeroes, not unknowns.
		a.s.Compactions = int64p(a.compactions)

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
