package declared

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    string // agent, or "" for no declaration
	}{
		{
			name: "copilot coding agent trailer",
			message: "Fix the thing\n\n" +
				"Agent-Logs-Url: https://github.com/o/r/actions/runs/1",
			want: AgentCopilot,
		},
		{
			name:    "copilot co-author line",
			message: "Fix the thing\n\nCo-authored-by: Copilot <copilot@github.com>",
			want:    AgentCopilot,
		},
		{
			name:    "cursor",
			message: "Fix the thing\n\nMade-with: Cursor",
			want:    AgentCursor,
		},
		{
			// The case that decides whether this package is safe to ship.
			// Co-authored-by is a decade-old convention written by people
			// about people; matching the trailer without checking who it
			// names would attribute half the open-source world to an agent.
			name:    "human co-author is not a declaration",
			message: "Fix the thing\n\nCo-authored-by: Alice <alice@example.com>",
			want:    "",
		},
		{
			name:    "no trailers at all",
			message: "Fix the thing",
			want:    "",
		},
		{
			name:    "empty message",
			message: "",
			want:    "",
		},
		{
			// Prose about a tool is not a claim by it. Without the colon
			// check, a commit explaining a Copilot bug would be attributed
			// to Copilot.
			name:    "mentioning an agent in prose is not a declaration",
			message: "Revert the Copilot co-author default\n\nIt fired on commits where Copilot was off.",
			want:    "",
		},
		{
			name:    "case and whitespace are not significant",
			message: "Fix\n\n   co-authored-by:   COPILOT <copilot@github.com>   ",
			want:    AgentCopilot,
		},
		{
			// Two agents can genuinely touch one commit. Something must
			// win deterministically rather than by map order, and a
			// dedicated trailer beats a co-author line.
			name: "competing signals resolve by strength, not by order",
			message: "Fix\n\n" +
				"Co-authored-by: Copilot <copilot@github.com>\n" +
				"Made-with: Cursor",
			want: AgentCursor,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.message)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected no declaration, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected agent %q, got no declaration", tc.want)
			}
			if got.Agent != tc.want {
				t.Errorf("agent = %q, want %q", got.Agent, tc.want)
			}
			if got.Signal == "" {
				t.Error("declaration must name the signal it matched")
			}
		})
	}
}

// A declaration says an agent was involved. It says nothing about which
// lines, so nothing here may grow a ratio, a session or a model — filling
// those from a trailer would be inventing evidence.
func TestDeclarationCarriesNoLineLevelEvidence(t *testing.T) {
	d := Parse("Fix\n\nMade-with: Cursor")
	if d == nil {
		t.Fatal("expected a declaration")
	}
	// Asserted structurally: if someone adds a Ratio field, this fails and
	// they have to justify it rather than quietly shipping a zero.
	if got := (Declaration{Agent: d.Agent, Signal: d.Signal}); got != *d {
		t.Errorf("Declaration grew a field beyond Agent and Signal: %+v", d)
	}
}

// This package must not become an adapter.
//
// adapter.Adapter is transcript-shaped — SessionDir, Root, SessionFiles,
// ParseSince — and a commit trailer has none of those. Implementing it
// would mean four methods returning empty and a fifth doing the work, and
// the registry would then treat a declaration as though it came from a
// session. Importing the package at all is the first step down that road,
// so the boundary is asserted rather than assumed.
func TestThisPackageDoesNotDependOnAdapter(t *testing.T) {
	// Parsed rather than grepped: this test's own prose mentions the
	// package it forbids, and a substring search would flag itself.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				if strings.Contains(imp.Path.Value, "internal/adapter") {
					t.Errorf("%s imports %s: a trailer reader is a "+
						"second producer, not an adapter", name, imp.Path.Value)
				}
			}
		}
	}
}
