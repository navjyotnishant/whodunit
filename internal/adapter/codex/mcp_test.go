package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every case here is a form observed in real rollouts on a developer
// machine, with the count that form appeared. The counts are what makes
// this a regression test rather than a restatement of the code: the bug
// was not that the prefixed form was handled wrongly, it was that the
// namespace form was 44% of MCP traffic and invisible.
func TestMCPToolRecognisesBothTaggingForms(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		tool      string
		wantName  string
		wantMCP   bool
		note      string
	}{
		{
			name:     "prefixed name, no namespace",
			tool:     "mcp__linear__save_comment",
			wantName: "mcp__linear__save_comment",
			wantMCP:  true,
			note:     "311 calls measured",
		},
		{
			name:      "namespace carries the server, name is bare",
			namespace: "mcp__linear",
			tool:      "save_comment",
			wantName:  "mcp__linear__save_comment",
			wantMCP:   true,
			note:      "83 calls measured — the single most common missed form",
		},
		{
			name:      "nested server namespace",
			namespace: "mcp__codex_apps__linear",
			tool:      "_save_comment",
			wantName:  "mcp__codex_apps__linear___save_comment",
			wantMCP:   true,
			note:      "27 calls measured",
		},
		{
			name:      "namespace that is not MCP",
			namespace: "multi_agent_v1",
			tool:      "spawn",
			wantName:  "spawn",
			wantMCP:   false,
			note:      "9 calls measured — a namespace alone must not imply MCP",
		},
		{
			name:     "ordinary local tool",
			tool:     "shell",
			wantName: "shell",
			wantMCP:  false,
		},
		{
			name:      "namespace present but empty name",
			namespace: "mcp__node_repl",
			wantName:  "mcp__node_repl",
			wantMCP:   true,
			note:      "server is still known even with no method",
		},
		{
			name:     "nothing at all",
			wantName: "",
			wantMCP:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotMCP := mcpTool(tc.namespace, tc.tool)
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q (%s)", gotName, tc.wantName, tc.note)
			}
			if gotMCP != tc.wantMCP {
				t.Errorf("isMCP = %v, want %v (%s)", gotMCP, tc.wantMCP, tc.note)
			}
		})
	}
}

// The namespace form must not merge with a same-named local tool. Before
// the fix the tool set keyed on the bare name, so an MCP `save_comment`
// and a local `save_comment` were one entry.
func TestNamespacedToolDoesNotCollideWithLocalToolOfSameName(t *testing.T) {
	mcpName, _ := mcpTool("mcp__linear", "save_comment")
	localName, isMCP := mcpTool("", "save_comment")

	if mcpName == localName {
		t.Fatalf("MCP and local tool both keyed as %q — they would merge in the tool set", mcpName)
	}
	if isMCP {
		t.Error("a bare name with no namespace was counted as MCP")
	}
}

// End to end through ParseSessionActivity, because mcpTool being right
// is not the same as the counter being wired to it.
func TestParseSessionActivityCountsNamespacedMCPCalls(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	lines := []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{
			"id": "sess-1", "cwd": "/tmp/repo", "cli_version": "0.1.0",
		}},
		// prefixed form
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "mcp__linear__list_issues",
		}},
		// namespace form — the one that used to be missed
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "namespace": "mcp__linear", "name": "save_comment",
		}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "namespace": "mcp__node_repl", "name": "js",
		}},
		// a namespace that is not MCP
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "namespace": "multi_agent_v1", "name": "spawn",
		}},
		// an ordinary tool
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "name": "shell",
		}},
	}

	path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	sessions, err := ParseSessionActivity(path, ts.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]

	if s.ToolCalls != 5 {
		t.Errorf("ToolCalls = %d, want 5", s.ToolCalls)
	}
	// 3 MCP: one prefixed, two namespaced. multi_agent_v1 and shell are not.
	if s.MCPCalls != 3 {
		t.Errorf("MCPCalls = %d, want 3 — namespaced calls are being dropped", s.MCPCalls)
	}
}
