// Author: Navjyot Nishant
// Created: 2026-08-24
// Last updated: 2026-08-24
// Description: Reading an agent's own declaration out of a commit message.

// Package declared reads the declarations agents write into commit messages.
//
// It exists because some agents leave no local transcript. GitHub Copilot
// writes its coding agent's commits with an `Agent-Logs-Url` trailer and
// itself as author; VS Code has shipped a `Co-authored-by: Copilot` line;
// Cursor writes `Made-with: Cursor`. None of that is a session file, so the
// adapter interface — SessionDir, Root, SessionFiles, ParseSince — has
// nothing to bind to. This is a second producer alongside adapters, not an
// adapter, and it is deliberately not registered as one.
//
// What it yields is the weakest rung the spec defines. A trailer is the
// author's own assertion about their own commit, verified by nothing, which
// is the definition of `declared`. VS Code made that concrete in 2026:
// version 1.118 added the co-author line by default and the setting was
// reverted a month later, in part because the trailer appeared on commits
// made with Copilot disabled. A self-applied label is evidence, and it is
// the least of it.
package declared

import (
	"strings"
)

// Agent identifiers, matching the vocabulary adapter.Name() uses so a
// consumer never has to know whether a name came from a transcript or a
// trailer.
const (
	AgentCopilot = "copilot"
	AgentCursor  = "cursor"
)

// Declaration is an agent's own claim to have worked on a commit.
//
// It carries no ratio, no session and no model. A trailer says an agent was
// involved and nothing about which lines it produced, so those stay absent
// rather than being filled with a zero that would assert the agent
// contributed nothing (NAV-21).
type Declaration struct {
	Agent string

	// Signal is the key that matched, kept so a report can say what the
	// claim rests on rather than only that something matched.
	Signal string
}

// signal is one recognised way an agent announces itself.
type signal struct {
	// Key is matched at the start of a line, case-insensitively, and must
	// be followed by a colon: these are git trailers, and a line merely
	// mentioning one in prose is not a declaration.
	Key string

	// Value, when set, must also appear in the trailer's value. It is what
	// separates `Co-authored-by: Copilot` from a human co-author, which is
	// the same trailer carrying an ordinary collaborator.
	Value string

	Agent string
}

// signals is the whole recognised set, ordered strongest evidence first.
//
// A dedicated trailer an agent writes for itself outranks a co-author line,
// because the co-author trailer predates all of this and is written by
// people about people far more often than by tools about themselves.
//
// Adding an agent is one entry here. Anything requiring more than an entry
// is a sign the signal is not really a declaration.
var signals = []signal{
	{Key: "agent-logs-url", Agent: AgentCopilot},
	{Key: "made-with", Value: "cursor", Agent: AgentCursor},
	{Key: "co-authored-by", Value: "copilot", Agent: AgentCopilot},
}

// Parse reads a commit message and returns the agent that declared itself,
// or nil when none did.
//
// Nil means no declaration, which is not the same as no agent: a commit
// with no trailer may still have been written with one that leaves no
// trace. The caller reports undetermined in that case, never "no AI"
// (NAV-21).
//
// Only the message is read. Not the diff, not the files, not anything the
// agent said — a declaration is a line of metadata, and this package cannot
// see prompt text or file contents because it is never given them (NAV-25).
func Parse(message string) *Declaration {
	if message == "" {
		return nil
	}
	for _, s := range signals {
		for _, line := range strings.Split(message, "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(key), s.Key) {
				continue
			}
			if s.Value != "" &&
				!strings.Contains(strings.ToLower(value), s.Value) {
				continue
			}
			return &Declaration{Agent: s.Agent, Signal: s.Key}
		}
	}
	return nil
}
