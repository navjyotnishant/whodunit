// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Reads Codex CLI rollout transcripts into journal entries.

// Package codex adapts Codex CLI's local session transcripts
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl) into journal.Entry records.
//
// Only apply_patch calls are read. Codex also edits files by running shell
// commands (`sed -i`, heredoc writes), and those are deliberately ignored:
// the path lives inside an arbitrary shell string, and guessing at it would
// produce confident wrong attribution — worse than none (NAV-21).
//
// Unlike Claude Code, Codex does not encode the repository into a directory
// name. Sessions are filed by date, and each one records its own working
// directory in a session_meta record, so finding a repository's sessions
// means opening candidate files rather than listing a directory.
//
// See docs/adapters/agent-support.md for the survey behind this.
package codex

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

const AgentName = "codex"

// SessionsDir returns the root Codex stores rollout transcripts under.
func SessionsDir() string {
	path, _ := adapter.ResolveRoot(AgentName, builtinSessionsDir())
	return path
}

func builtinSessionsDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// record is the subset of a rollout line this adapter needs.
type record struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMeta is the first record of a rollout: which repository the
// session ran in, and which Codex wrote it.
type sessionMeta struct {
	ID         string `json:"id"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
}

// toolCall is an apply_patch invocation. Codex records edits as a
// custom_tool_call whose Input is the patch text itself, not JSON.
// mcpTool resolves a tool call's display name and whether it went through
// MCP, from the two ways Codex tags one.
//
// The first way is a prefixed name: name="mcp__linear__save_comment", no
// namespace. The second is a bare name with the server in a separate
// field: namespace="mcp__linear", name="save_comment". Measured on this
// machine, 311 calls used the first form and 243 used the second — and
// only the first was ever counted, so mcp_calls read 44% low for every
// Codex user.
//
// The namespace is not by itself an MCP marker. Codex also uses it for
// built-ins ("multi_agent_v1", 9 calls), so the mcp__ prefix is what
// decides, on whichever field carries it.
//
// The returned name is qualified as server__tool for the namespace form.
// Left bare, a `save_comment` from an MCP server and a local tool of the
// same name would merge into one entry in the tool set.
func mcpTool(namespace, name string) (string, bool) {
	const prefix = "mcp__"
	switch {
	case strings.HasPrefix(name, prefix):
		return name, true
	case strings.HasPrefix(namespace, prefix):
		if name == "" {
			return namespace, true
		}
		return namespace + "__" + name, true
	default:
		return name, false
	}
}

type toolCall struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	CallID string `json:"call_id"`
	Input  string `json:"input"`
}

// toolOutput is what happened to a call. The output field is either a plain
// string or a JSON object with its own "output" key, depending on Codex
// version, so it is decoded permissively.
type toolOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// SessionFiles returns every rollout transcript recorded for the repository
// at cwd.
//
// Every transcript has to be opened, because Codex files sessions by date
// rather than by repository — only the session_meta record inside says
// which directory it ran in. Reading one short line per file keeps that
// affordable; the alternative is an index whodunit would have to maintain
// and keep correct.
func SessionFiles(cwd string) ([]string, error) {
	root := SessionsDir()
	if root == "" {
		return nil, nil
	}
	target, err := filepath.Abs(cwd)
	if err != nil {
		target = cwd
	}

	var out []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is skipped rather than
			// failing the walk: one unreadable day must not hide every
			// other session.
			return nil //nolint:nilerr // deliberate: keep walking
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if meta, ok := readMeta(path); ok && sameDir(meta.CWD, target) {
			out = append(out, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil // Codex not installed: a fact, not a failure
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readMeta reads just the session_meta record, which Codex writes first.
// Scans a bounded number of lines rather than the whole file: this runs
// across every transcript on the machine, and a session that never declares
// its directory is not one this adapter can use anyway.
func readMeta(path string) (sessionMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return sessionMeta{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for i := 0; scanner.Scan() && i < 5; i++ {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Type != "session_meta" {
			continue
		}
		var m sessionMeta
		if err := json.Unmarshal(r.Payload, &m); err != nil {
			return sessionMeta{}, false
		}
		return m, m.CWD != ""
	}
	return sessionMeta{}, false
}

// sameDir compares two directory paths, tolerating a trailing separator and
// the /private prefix macOS adds to temp directories.
func sameDir(a, b string) bool {
	na, nb := normalizeDir(a), normalizeDir(b)
	if na == nb {
		return true
	}

	// Compare without the volume when only one side has one.
	//
	// A transcript records the cwd the agent saw — "/repo" from a
	// Unix-flavoured shell — while the directory it is compared against has
	// been through filepath.Abs, which on Windows prepends the current
	// drive and yields "C:/repo". Same place, and requiring the volumes to
	// match meant a session never recognised its own repository.
	//
	// Not applied when both carry a volume, so C:/repo and D:/repo stay
	// distinct.
	va, vb := volumeOf(na), volumeOf(nb)
	if va == vb || (va != "" && vb != "") {
		return false
	}
	return strings.TrimPrefix(na, va) == strings.TrimPrefix(nb, vb)
}

// volumeOf returns the "C:" of a path, or "" when it carries no volume.
func volumeOf(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return p[:2]
	}
	return ""
}

// isAbsolutePath recognises an absolute path in either platform's spelling.
//
// filepath.IsAbs answers for the host only: on Windows "/repo/main.go" is
// not absolute, because absolute there means a drive letter or a UNC share.
// A transcript written on one machine and read on another is exactly the
// case this package handles, and treating a Unix absolute path as relative
// joined it to the session cwd — turning /abs/new.py into C:\repo\abs\new.py
// and losing the file it referred to.
func isAbsolutePath(p string) bool {
	if p == "" {
		return false
	}
	if filepath.IsAbs(p) {
		return true
	}
	// A leading separator: Unix-absolute, or a UNC path.
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	// A drive letter, spelled with either separator: "C:/x" or "C:\x".
	return len(p) >= 3 && p[1] == ':' &&
		(p[2] == '/' || p[2] == '\\') &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
}

func normalizeDir(p string) string {
	// Resolved with the host's own semantics where that works — EvalSymlinks
	// is what makes /private/tmp and /tmp compare equal on macOS — and then
	// reduced to a separator-neutral string for the comparison itself.
	//
	// The second half matters on Windows: a transcript records its cwd as
	// "/repo" while the directory being compared arrives as "C:\repo", and
	// leaving the two spellings distinct meant a session never matched its
	// own repository, so nothing was ever attributed.
	if resolved, err := filepath.EvalSymlinks(filepath.FromSlash(p)); err == nil {
		p = resolved
	}
	p = strings.ReplaceAll(filepath.Clean(p), `\`, "/")
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// ParseSince reads every apply_patch edit at or after `since` from one
// rollout transcript, along with what happened to each call.
//
// Two passes, for the same reason as Claude Code: a call and its result are
// separate records and the result arrives later, so outcomes are collected
// first and joined by call_id.
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	// A transcript untouched since the cutoff has nothing to contribute.
	// Checked before anything is read: collecting outcomes below is a full
	// pass over the file, so without this a no-op ingest costs more than a
	// real one. That matters here more than anywhere — Codex keeps every
	// session on the machine in one tree, and ingest walks all of them.
	if !since.IsZero() {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(since) {
			return nil, nil
		}
	}

	meta, _ := readMeta(path)
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
		if r.Type != "response_item" || r.Timestamp.Before(since) {
			continue
		}
		var c toolCall
		if err := json.Unmarshal(r.Payload, &c); err != nil {
			continue
		}
		if c.Type != "custom_tool_call" || c.Name != "apply_patch" {
			// Every other call is recorded by name alone: exec_command,
			// tool_search_call, request_plugin_install, and whatever Codex
			// adds later.
			//
			// c.Input is deliberately not read. A shell command line is
			// the clearest case — it routinely contains file contents via
			// a heredoc — and the same risk applies to any tool payload
			// (NAV-25).
			if name := c.Name; name != "" && c.Type != "" {
				entries = append(entries, journal.Entry{
					Timestamp:    r.Timestamp,
					Agent:        AgentName,
					AgentVersion: meta.CLIVersion,
					Session:      meta.ID,
					Event:        "tool_call",
					Tool:         name,
				})
			}
			continue
		}

		outcome, ok := outcomes[c.CallID]
		if !ok {
			// No result recorded: the session may still be running, or the
			// transcript truncated. Unknown rather than assumed accepted,
			// which would flatter the acceptance rate.
			outcome = OutcomeUnknown
		}

		for _, fp := range parsePatch(c.Input) {
			file := fp.Path
			if !isAbsolutePath(file) && meta.CWD != "" {
				// Patch paths are relative to the session's directory.
				//
				// Normalised to forward slashes afterwards: filepath.Join
				// produces backslashes on Windows, while every other
				// producer of this field — the transcript itself, the other
				// adapters — uses forward slashes. The journal, the reports
				// and the dashboards all group by this string, so two
				// spellings of one path split a file's history in two.
				file = filepath.ToSlash(filepath.Join(meta.CWD, file))
			}

			added, removed := fp.Added, fp.Removed
			lines := linehash.OfText(linehash.Canonical(file), strings.Join(fp.AddedLines, "\n"))
			// A rejected or failed patch never reached the file, so its
			// text must not count as agent-authored code.
			if outcome != OutcomeAccepted {
				lines = nil
				added, removed = 0, 0
			}

			entries = append(entries, journal.Entry{
				Timestamp:    r.Timestamp,
				Agent:        AgentName,
				AgentVersion: meta.CLIVersion,
				Session:      meta.ID,
				Event:        "tool_use",
				Tool:         "apply_patch",
				File:         file,
				LinesAdded:   added,
				LinesRemoved: removed,
				HunkHash:     hunkHash(file, fp.AddedLines),
				LineHashes:   lines,
				Outcome:      string(outcome),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ParseSessionActivity summarises engagement for one rollout (NAV-55).
// Counts only — no message text is read.
func ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	meta, ok := readMeta(path)
	if !ok {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := journal.Session{
		Session:      meta.ID,
		Agent:        AgentName,
		AgentVersion: meta.CLIVersion,
	}
	tools := map[string]bool{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Timestamp.Before(since) {
			continue
		}
		if s.FirstSeen.IsZero() || r.Timestamp.Before(s.FirstSeen) {
			s.FirstSeen = r.Timestamp
		}
		if r.Timestamp.After(s.LastSeen) {
			s.LastSeen = r.Timestamp
		}
		// Codex writes three record types worth reading, and this used to
		// read one (NAV-87).
		//
		// `type != "response_item" { continue }` discarded 18,142
		// event_msg and 4,089 turn_context records across 125 rollouts —
		// about 40% of every transcript, and specifically the 40% holding
		// tokens, timing, model, effort and approval policy. Not a missing
		// field: a missing record type.
		switch r.Type {
		case "event_msg":
			readEventMsg(&s, r.Payload)
			continue
		case "turn_context":
			readTurnContext(&s, r.Payload)
			continue
		case "response_item":
			// handled below
		default:
			continue
		}

		var item struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Role string `json:"role"`
			// Codex tags an MCP call in one of two ways, never both, and
			// the second one used to be invisible here — see mcpTool.
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(r.Payload, &item); err != nil {
			continue
		}
		switch item.Type {
		case "message":
			if item.Role == "user" {
				s.UserMessages++
			} else {
				s.AgentMessages++
			}
		case "function_call", "custom_tool_call":
			s.ToolCalls++
			name, isMCP := mcpTool(item.Namespace, item.Name)
			if name != "" {
				tools[name] = true
			}
			if isMCP {
				s.MCPCalls++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if s.Session == "" {
		return nil, nil
	}

	// A session whose every record predates the cutoff is not a session
	// with nothing in it — it is a session this window cannot see.
	//
	// s.Session comes from the rollout's session_meta, which is read
	// before the cutoff is applied, so without this a `dun ingest --since`
	// over a recent window wrote a row per historical rollout: all
	// counters zero, and FirstSeen the zero time, which reaches SQLite as
	// -6795364578871345152. Measured on a real repository: 74 Codex
	// sessions written, every one of them empty.
	//
	// Worse than useless, because those rows then take part in averages —
	// a per-session token average is divided by a denominator mostly made
	// of sessions that were never read (NAV-21).
	if s.FirstSeen.IsZero() {
		return nil, nil
	}

	s.DistinctTools = len(tools)
	return []journal.Session{s}, nil
}

// hunkHash identifies one file's produced text within a patch, so the same
// edit ingested twice is recognised rather than duplicated.
func hunkHash(file string, added []string) string {
	if len(added) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(file + "\x00" + strings.Join(added, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
