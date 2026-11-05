// Author: Navjyot Nishant
// Created: 2026-11-05
// Last updated: 2026-11-05
// Description: Adapts Desktop Commander's tool history into journal entries.

// Package desktopcommander reads the tool history Desktop Commander, an MCP
// server that gives an AI client file and shell access to this machine,
// keeps at ~/.claude-server-commander/tool-history.jsonl.
//
// It exists for agents whose transcript never reaches this machine. Claude
// Desktop's cloud Cowork sessions are the case that prompted it: the model
// runs in Anthropic's cloud and edits files here through this server, so the
// server's own history is the only local record of what it wrote. Any other
// client using the server is recorded the same way.
//
// Only write_file and edit_block are read. Each carries the text written,
// which is hashed line by line exactly as Claude Code's Write and Edit are,
// so these edits can reach `intersected`. Shell commands run through the
// server are also recorded, but a command is not a diff; files they write
// are named, never hashed.
//
// What the history does not have, and so neither do the entries: a session
// id, a model, the client's name or version, and per-turn usage. The agent is
// recorded as desktop-commander because that is all the record proves.
package desktopcommander

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/adapter/claudecode"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/linehash"
)

// AgentName names the bridge, not the model behind it. The history does not
// say which client made a call, and guessing would credit the wrong agent.
const AgentName = "desktop-commander"

// historyFile is the server's tool history. The server keeps it short: on
// the survey machine it held under a day of calls, so it has to be ingested
// often or edits fall off the end. The daemon's poll does that.
const historyFile = "tool-history.jsonl"

// Dir is the server's data directory.
func Dir() string {
	path, _ := adapter.ResolveRoot(AgentName, builtinDir())
	return path
}

func builtinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude-server-commander")
}

// SessionFiles returns the history file, but only when it mentions the
// repository at cwd.
//
// One history serves every repository on the machine, so returning it
// unconditionally would tell the commit hook that a store was searched for
// every repository, and a repository nobody watched would be stamped
// `unassisted` instead of `undetermined` (WHO-236). A byte search for the
// repository path is cheap, and a file that never names the repository
// cannot hold an edit to it.
func SessionFiles(cwd string) ([]string, error) {
	dir := Dir()
	if dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, historyFile)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	repo := linehash.Canonical(cwd)
	names := []string{repo}
	if plain := filepath.ToSlash(filepath.Clean(cwd)); plain != repo {
		names = append(names, plain)
	}
	// Matched as an edit's own path argument, not anywhere in the file: a
	// shell command that cd's into the repository, or a read of one of its
	// files, names it too, and counting that would claim the repository was
	// watched for edits when the history only shows it was visited.
	if editsUnder(raw, names) {
		setScope(path, names)
		return []string{path}, nil
	}
	return nil, nil
}

var editTools = [][]byte{[]byte(`"toolName":"write_file"`), []byte(`"toolName":"edit_block"`)}

// editsUnder reports whether any write_file or edit_block line in the
// history targets a path under one of the repository's spellings. Byte
// matching on the line, not a JSON decode: this runs on every commit over a
// history that reaches megabytes.
func editsUnder(raw []byte, names []string) bool {
	var needles [][]byte
	for _, n := range names {
		needles = append(needles, []byte(`"path":"`+n+"/"), []byte(`"file_path":"`+n+"/"))
	}
	for len(raw) > 0 {
		line := raw
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			line, raw = raw[:i], raw[i+1:]
		} else {
			raw = nil
		}
		isEdit := false
		for _, t := range editTools {
			if bytes.Contains(line, t) {
				isEdit = true
				break
			}
		}
		if !isEdit {
			continue
		}
		for _, n := range needles {
			if bytes.Contains(line, n) {
				return true
			}
		}
	}
	return false
}

// scope is the repository the history was last listed for. Parsing keeps
// only edits inside it, because the history holds every repository's.
var (
	scope   = map[string][]string{}
	scopeMu sync.Mutex
)

func setScope(path string, repo []string) {
	scopeMu.Lock()
	scope[path] = repo
	scopeMu.Unlock()
}

func scopeOf(path string) []string {
	scopeMu.Lock()
	defer scopeMu.Unlock()
	return scope[path]
}

// record is one line of the history. arguments are declared field by field
// so nothing else in a call, such as a search pattern or a process's
// input, is ever decoded.
type record struct {
	Timestamp time.Time `json:"timestamp"`
	ToolName  string    `json:"toolName"`
	Arguments struct {
		Path      string `json:"path"`      // write_file
		Content   any    `json:"content"`   // write_file text; a sheet range on edit_block
		Mode      string `json:"mode"`      // write_file: rewrite or append
		FilePath  string `json:"file_path"` // edit_block
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Command   string `json:"command"` // start_process
	} `json:"arguments"`
	Output *struct {
		IsError bool `json:"isError"`
	} `json:"output"`
}

// ParseSince turns the history's edits at or after since into journal
// entries, keeping only files inside the repository the history was listed
// for.
func ParseSince(path string, since time.Time) ([]journal.Entry, error) {
	if !since.IsZero() {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(since) {
			return nil, nil
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	repos := scopeOf(path)
	inRepo := func(p string) bool {
		if p == "" || !filepath.IsAbs(p) {
			return false
		}
		c := linehash.Canonical(p)
		for _, r := range repos {
			if strings.HasPrefix(c, strings.TrimSuffix(r, "/")+"/") {
				return true
			}
		}
		// Listed for no repository: nothing can be placed safely.
		return false
	}

	var out []journal.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Timestamp.Before(since) {
			continue
		}
		switch r.ToolName {
		case "write_file", "edit_block":
			if e, ok := editEntry(r); ok && inRepo(e.File) {
				out = append(out, e)
			}
		case "start_process":
			// A shell command names the files it writes, at best. No
			// cwd is recorded, so only absolute targets can be placed.
			targets, incomplete := claudecode.WriteTargets(r.Arguments.Command, "")
			for _, t := range targets {
				if !inRepo(t) {
					continue
				}
				e := base(r)
				e.Tool = r.ToolName
				e.File = t
				if incomplete {
					e.Outcome = string(claudecode.OutcomePartial)
				}
				out = append(out, e)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// editEntry builds the entry for one write_file or edit_block call. False
// for a call that wrote no text: an edit_block on a spreadsheet range, or a
// write with no content.
func editEntry(r record) (journal.Entry, bool) {
	e := base(r)
	e.Tool = r.ToolName
	var produced, removed string
	switch r.ToolName {
	case "write_file":
		text, ok := r.Arguments.Content.(string)
		if !ok {
			return e, false
		}
		e.File, produced = r.Arguments.Path, text
	case "edit_block":
		if r.Arguments.FilePath == "" || (r.Arguments.NewString == "" && r.Arguments.OldString == "") {
			return e, false
		}
		e.File, produced, removed = r.Arguments.FilePath, r.Arguments.NewString, r.Arguments.OldString
	}
	if e.File == "" {
		return e, false
	}

	// The output says whether the call worked. A missing output is not
	// success: recorded as unknown, which carries no line hashes, so an
	// edit that may never have reached the file cannot claim one.
	switch {
	case r.Output == nil:
		e.Outcome = string(claudecode.OutcomeUnknown)
	case r.Output.IsError:
		e.Outcome = string(claudecode.OutcomeFailed)
	default:
		e.Outcome = string(claudecode.OutcomeAccepted)
	}
	if e.Outcome == string(claudecode.OutcomeAccepted) {
		e.LinesAdded, e.LinesRemoved = countLines(produced), countLines(removed)
		e.LineHashes = linehash.OfText(linehash.Canonical(e.File), produced)
	}
	return e, true
}

// base is what every entry from the history shares.
//
// The history has no session, so one is made per UTC day. It is a grouping
// for the journal, which requires one, and claims nothing more.
func base(r record) journal.Entry {
	return journal.Entry{
		Timestamp: r.Timestamp,
		Agent:     AgentName,
		Session:   AgentName + "-" + r.Timestamp.UTC().Format("2006-01-02"),
		Event:     "tool_use",
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// ParseSessionActivity returns nothing: the history records no sessions,
// messages or tokens, and a row of zeroes would claim they were measured.
func ParseSessionActivity(string, time.Time) ([]journal.Session, error) { return nil, nil }
