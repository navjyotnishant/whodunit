// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: Reads Antigravity CLI (agy) conversation databases into
// journal entries.

// Package agy adapts Antigravity CLI's local conversation stores
// (~/.gemini/antigravity-cli/conversations/<uuid>.db) into journal.Entry
// records.
//
// Unlike every other adapter, the store is SQLite rather than JSONL: one
// database per conversation, with a steps table holding one row per agent
// action. The payloads are protobuf, but not encrypted — tool arguments
// appear as embedded JSON, which is what this reads.
//
// The Antigravity IDE shares the parent directory and is a different thing
// entirely: it encrypts its conversation bodies. This package covers only
// the CLI.
//
// Two cautions that shaped the implementation:
//
// Reading requires the write-ahead log. A conversation's rows can live
// entirely in <db>-wal, so opening the main database alone returns zero
// steps for a session that plainly has them — silently reporting "no AI
// activity" for work that happened (NAV-21).
//
// A conversation that is currently open can be mid-write, and SQLite will
// report the file as malformed. That degrades to no entries for that one
// conversation rather than failing the ingest.
//
// See docs/adapters/agent-support.md for the survey behind this.
package agy

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
	_ "modernc.org/sqlite"
)

const AgentName = "agy"

// ConversationsDir returns the directory agy stores conversation databases
// in.
func ConversationsDir() string {
	path, _ := adapter.ResolveRoot(AgentName, builtinConversationsDir())
	return path
}

func builtinConversationsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
}

// SessionFiles returns every conversation database that touched a file
// under cwd.
//
// agy records no workspace directory of its own that can be trusted: the
// CLI's conversations do not appear in conversation_summaries.db, and the
// URIs in its prose are unreliable. What is reliable is the absolute
// TargetFile on each edit, so a conversation belongs to a repository when
// it edited a file inside it.
//
// That means opening each database. There are few of them, and the
// alternative is an index whodunit would have to maintain.
func SessionFiles(cwd string) ([]string, error) {
	root := ConversationsDir()
	if root == "" {
		return nil, nil
	}
	target, err := filepath.Abs(cwd)
	if err != nil {
		target = cwd
	}
	target = resolve(target)

	dbs, err := filepath.Glob(filepath.Join(root, "*.db"))
	if err != nil {
		return nil, err
	}
	if len(dbs) == 0 {
		return nil, nil // not installed, or never used: a fact, not a failure
	}

	var out []string
	for _, db := range dbs {
		if touchesRepo(db, target) {
			out = append(out, db)
		}
	}
	return out, nil
}

// touchesRepo reports whether a conversation edited any file under repo.
// A database that cannot be read answers false rather than failing: one
// locked conversation must not hide the others.
func touchesRepo(dbPath, repo string) bool {
	calls, err := readCalls(dbPath)
	if err != nil {
		return false
	}
	for _, c := range calls {
		if c.TargetFile != "" && under(resolve(c.TargetFile), repo) {
			return true
		}
	}
	return false
}

// under reports whether path sits inside dir.
//
// Compared as normalised strings rather than through filepath.Rel. Rel
// answers with the host's rules, and on Windows those include a volume: a
// transcript records "/repo/main.go" while the working directory is
// "C:\repo", so Rel either errors or returns a "..\" path and every session
// looked like it belonged to another repository. Nothing matched, and the
// result was attribution that silently found no sessions at all.
//
// Trailing separators are trimmed so "/repo" and "/repo/" behave alike, and
// the boundary is checked explicitly so "/repository" is not treated as
// living inside "/repo".
func under(path, dir string) bool {
	p := normalizeForCompare(path)
	d := normalizeForCompare(dir)
	if d == "" {
		return false
	}
	if p == d || strings.HasPrefix(p, d+"/") {
		return true
	}

	// Fall back to comparing without the volume.
	//
	// A conversation records the path the agent saw, which may have been
	// written on another machine or in another shell: "/repo/main.go" from
	// a Unix-flavoured environment against a working directory that
	// filepath.Abs has turned into "C:/repo" by prepending the current
	// drive. Those denote the same place, and requiring the volumes to
	// match meant no conversation was ever attributed on Windows.
	//
	// Only applied when one side carries a volume and the other does not,
	// so C:/repo and D:/repo stay distinct.
	pv, dv := volumeOf(p), volumeOf(d)
	if pv == dv || (pv != "" && dv != "") {
		return false
	}
	p, d = strings.TrimPrefix(p, pv), strings.TrimPrefix(d, dv)
	return p == d || strings.HasPrefix(p, d+"/")
}

// volumeOf returns the "C:" of a path, or "" when it carries no volume.
func volumeOf(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return p[:2]
	}
	return ""
}

func normalizeForCompare(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// resolve canonicalises a path so /private/tmp and /tmp compare equal on
// macOS. A path that cannot be resolved is returned cleaned.
func resolve(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// toolArgs is the JSON embedded in a step payload. Field names come from
// the two edit tools agy uses; anything else is ignored.
type toolArgs struct {
	// replace_file_content
	TargetFile         string `json:"TargetFile"`
	TargetContent      string `json:"TargetContent"`
	ReplacementContent string `json:"ReplacementContent"`
	StartLine          int    `json:"StartLine"`
	EndLine            int    `json:"EndLine"`

	// write_file
	CodeContent string `json:"CodeContent"`

	// present on read-only tools, kept so they can be counted
	AbsolutePath string `json:"AbsolutePath"`
	ToolAction   string `json:"toolAction"`
}

// call is one tool invocation recovered from a step.
type call struct {
	Idx        int
	StepType   int
	TargetFile string
	Args       toolArgs
	Tool       string
}

// findJSONObjects returns every embedded JSON object in a protobuf
// payload, by scanning for balanced braces.
//
// A regular expression is the obvious approach and the wrong one here: Go's
// regexp caps a bounded repeat at 1000 characters, and a written file body
// is routinely longer than that, so the match would silently stop finding
// exactly the large edits that matter most.
//
// Brace depth is tracked with string literals and escapes honoured, so a
// brace inside written code does not end the object early.
func findJSONObjects(payload []byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(payload); i++ {
		// agy writes objects whose first key is a letter; anything else at
		// this position is protobuf framing rather than tool arguments.
		if payload[i] != '{' || i+2 >= len(payload) || payload[i+1] != '"' {
			continue
		}
		if c := payload[i+2]; !isLetter(c) {
			continue
		}
		if end, ok := scanObject(payload, i); ok {
			out = append(out, payload[i:end])
			i = end - 1
		}
	}
	return out
}

// scanObject returns the index just past the object starting at start.
func scanObject(b []byte, start int) (int, bool) {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(b); i++ {
		c := b[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// Braces inside a string literal are content, not structure.
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false // truncated payload: no complete object here
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// readCalls extracts every edit call from one conversation database.
func readCalls(dbPath string) ([]call, error) {
	db, cleanup, err := openWithWAL(dbPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	rows, err := db.Query(`SELECT idx, step_type, step_payload FROM steps ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []call
	for rows.Next() {
		var idx, stepType int
		var payload []byte
		if err := rows.Scan(&idx, &stepType, &payload); err != nil {
			continue
		}
		if len(payload) == 0 {
			continue
		}
		var isEdit bool
		for _, m := range findJSONObjects(payload) {
			var a toolArgs
			if err := json.Unmarshal(m, &a); err != nil {
				continue
			}
			tool := toolFor(a)
			if tool == "" {
				continue
			}
			isEdit = true
			out = append(out, call{Idx: idx, StepType: stepType, TargetFile: a.TargetFile, Args: a, Tool: tool})
		}

		// A step that produced no edit still names whatever tool it ran —
		// view_file, list_dir, run_command. Recorded by name alone, so the
		// journal shows what the agent did rather than only what it wrote.
		if !isEdit {
			for _, name := range callNames(payload) {
				out = append(out, call{Idx: idx, StepType: stepType, Tool: name})
			}
		}
	}
	return out, rows.Err()
}

// callNamePattern finds the tool name a step recorded.
//
// agy's payload is protobuf, and the name follows the call id as a length-
// prefixed string: `call_281815` then `view_file`. Matching the id first
// anchors the search, so this does not pick up arbitrary text elsewhere in
// the payload — which matters because these payloads also contain whatever
// file the agent happened to be reading.
var callNamePattern = regexp.MustCompile(`call_\d+.{0,3}?([a-z][a-z0-9_]{2,40})`)

// callNames returns the distinct tool names in a step payload.
//
// Names only. The arguments beside them routinely hold file contents — a
// view_file result, a write_to_file body — and this package does not
// collect those (NAV-25).
func callNames(payload []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range callNamePattern.FindAllSubmatch(payload, -1) {
		name := string(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// toolFor names the edit tool a set of arguments came from, or "" when the
// arguments are not an edit.
func toolFor(a toolArgs) string {
	switch {
	case a.TargetFile != "" && a.ReplacementContent != "":
		return "replace_file_content"
	case a.TargetFile != "" && a.CodeContent != "":
		return "write_file"
	default:
		return ""
	}
}

// openWithWAL opens a conversation database for reading, including rows
// that are still only in its write-ahead log.
//
// The database is copied first, with its -wal and -shm sidecars. Opening
// the original read-write would let SQLite checkpoint and modify a user's
// file; opening it immutable ignores the WAL and silently under-reports.
// Copying costs a little and avoids both.
func openWithWAL(dbPath string) (*sql.DB, func(), error) {
	tmp, err := os.MkdirTemp("", "whodunit-agy-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { os.RemoveAll(tmp) }

	dst := filepath.Join(tmp, "conversation.db")
	if err := copyFile(dbPath, dst); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	// Sidecars are best-effort: a conversation that is not open has none.
	for _, ext := range []string{"-wal", "-shm"} {
		_ = copyFile(dbPath+ext, dst+ext)
	}

	db, err := sql.Open("sqlite", dst)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		cleanup()
		return nil, func() {}, fmt.Errorf("open %s: %w", filepath.Base(dbPath), err)
	}
	return db, func() { db.Close(); cleanup() }, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// ParseSince turns one conversation database into journal entries.
//
// The steps table carries no timestamp per row, so the database's own
// modification time stands in for when the work happened. That is coarse —
// every entry in a conversation shares it — but it is honest: inventing
// per-step times would imply precision the store does not have.
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	ts := info.ModTime().UTC()
	if ts.Before(since) {
		return nil, nil
	}

	calls, err := readCalls(path)
	if err != nil {
		// A locked or half-written conversation yields nothing rather than
		// failing the whole ingest.
		return nil, nil
	}

	session := strings.TrimSuffix(filepath.Base(path), ".db")
	seen := map[string]bool{}
	var entries []journal.Entry

	for _, c := range calls {
		// A call that touched no file is recorded by name alone: no hunk
		// hash and no line hashes, because nothing reached a file for a
		// staged diff to match against.
		//
		// This has to come before the produced check below — a non-editing
		// call produces nothing by definition, so that guard would drop
		// every one of them.
		if c.TargetFile == "" {
			entries = append(entries, journal.Entry{
				Timestamp: ts,
				Agent:     AgentName,
				Session:   session,
				Event:     "tool_call",
				Tool:      c.Tool,
			})
			continue
		}

		produced := c.Args.ReplacementContent
		if c.Tool == "write_file" {
			produced = c.Args.CodeContent
		}
		if produced == "" {
			continue
		}

		// A call and its result carry identical arguments, so the same edit
		// appears twice. Deduplicate on what was produced rather than on
		// the step index, which differs between the pair.
		key := c.TargetFile + "\x00" + produced
		if seen[key] {
			continue
		}
		seen[key] = true

		added := countLines(produced)
		removed := countLines(c.Args.TargetContent)

		entries = append(entries, journal.Entry{
			Timestamp:    ts,
			Agent:        AgentName,
			Session:      session,
			Event:        "tool_use",
			Tool:         c.Tool,
			File:         c.TargetFile,
			LinesAdded:   added,
			LinesRemoved: removed,
			HunkHash:     hunkHash(c.TargetFile, produced),
			LineHashes:   linehash.OfText(linehash.Canonical(c.TargetFile), produced),
			// agy records no rejection signal: a declined call simply does
			// not appear. Recording accepted would assert something the
			// store does not say, so these stay unknown (NAV-21, NAV-54).
			Outcome: "unknown",
		})
	}
	return entries, nil
}

// ParseSessionActivity summarises one conversation (NAV-55). Counts only.
func ParseSessionActivity(path string, since time.Time) ([]journal.Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	ts := info.ModTime().UTC()
	if ts.Before(since) {
		return nil, nil
	}

	db, cleanup, err := openWithWAL(path)
	if err != nil {
		return nil, nil
	}
	defer cleanup()

	var steps int
	if err := db.QueryRow(`SELECT COUNT(*) FROM steps`).Scan(&steps); err != nil {
		return nil, nil
	}
	if steps == 0 {
		return nil, nil
	}

	calls, _ := readCalls(path)
	tools := map[string]bool{}
	for _, c := range calls {
		tools[c.Tool] = true
	}

	// Only tool calls are counted. agy stores no per-message record this
	// adapter can distinguish, so message counts are left at zero rather
	// than guessed — a real zero and a missing measurement are different
	// claims, and the dashboard shows the denominator.
	return []journal.Session{{
		Session:       strings.TrimSuffix(filepath.Base(path), ".db"),
		Agent:         AgentName,
		FirstSeen:     ts,
		LastSeen:      ts,
		ToolCalls:     steps,
		DistinctTools: len(tools),
	}}, nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n"))
}

func hunkHash(file, produced string) string {
	sum := sha256.Sum256([]byte(file + "\x00" + produced))
	return "sha256:" + hex.EncodeToString(sum[:])
}
