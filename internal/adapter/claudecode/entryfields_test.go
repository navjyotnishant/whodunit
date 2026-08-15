package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/journal"
)

// Model, branch and MCP server on the edit itself, so a report can ask
// which model wrote which line and on which branch it landed (NAV-89).
func TestEntriesCarryModelBranchAndMCPServer(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t, map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
		"gitBranch":            "feature/ENG-180-safety-net",
		"attributionMcpServer": "linear-server",
		"message": map[string]any{
			"id": "m1", "model": "claude-opus-5",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "Write",
					"input": map[string]any{
						"file_path": "/repo/x.go", "content": "package x\n",
					}},
			},
		},
	})

	e := oneEntry(t, path)

	if e.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want claude-opus-5", e.Model)
	}
	if e.Branch != "feature/ENG-180-safety-net" {
		t.Errorf("Branch = %q, want the feature branch", e.Branch)
	}
	if e.MCPServer != "linear-server" {
		t.Errorf("MCPServer = %q, want linear-server", e.MCPServer)
	}
}

// "HEAD" is a detached head — a real state the agent was in, not a missing
// value. Blanking it would lose the distinction between "no branch
// recorded" and "recorded as detached", which is exactly the conflation
// NAV-21 forbids. Measured at 8% of branch-bearing records here.
func TestDetachedHeadIsStoredRatherThanBlanked(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	e := oneEntry(t, writeUsageTranscript(t, writeToolUse(ts, "HEAD", "", "claude-opus-5")))

	if e.Branch != "HEAD" {
		t.Errorf("Branch = %q, want HEAD stored as written", e.Branch)
	}
}

// userModified is the one signal separating "the agent wrote this" from
// "the agent wrote this and it was kept". Only Claude Code has it.
func TestUserModifiedIsReadFromTheToolResultRecord(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)

	// It is recorded on the USER record carrying the tool_result, keyed by
	// the same tool_use_id — a sibling of message, not part of it.
	path := writeUsageTranscript(t,
		writeToolUse(ts, "main", "", "claude-opus-5"),
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"toolUseResult": map[string]any{"userModified": true},
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1",
					"content": "The file has been updated."},
			}},
		},
	)

	e := oneEntry(t, path)
	if e.UserModified == nil {
		t.Fatal("UserModified is nil; the signal was not read")
	}
	if !*e.UserModified {
		t.Error("UserModified = false, want true")
	}
}

// A call with no edit signal at all must stay nil. False is a claim that
// nobody edited it, which is a different statement from "we do not know"
// — and the difference decides whether an acceptance-quality metric can
// be computed at all (NAV-21).
func TestAToolCallWithNoEditSignalStaysNil(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		writeToolUse(ts, "main", "", "claude-opus-5"),
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			// A result with no userModified key at all.
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1",
					"content": "The file has been updated."},
			}},
		},
	)

	e := oneEntry(t, path)
	if e.UserModified != nil {
		t.Errorf("UserModified = %v for a call with no signal, want nil — false "+
			"asserts nobody edited it (NAV-21)", *e.UserModified)
	}
}

// An explicit false must be preserved as false, not collapsed into nil.
// The two are different answers and the whole point of the pointer is to
// keep them apart. Measured on this machine, every one of 7,201
// occurrences was false, so this is the case that actually occurs.
func TestAnExplicitFalseIsPreserved(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		writeToolUse(ts, "main", "", "claude-opus-5"),
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"toolUseResult": map[string]any{"userModified": false},
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1",
					"content": "The file has been updated."},
			}},
		},
	)

	e := oneEntry(t, path)
	if e.UserModified == nil {
		t.Fatal("an explicit false was collapsed to nil; 'nobody edited it' and " +
			"'we cannot tell' are different answers")
	}
	if *e.UserModified {
		t.Error("UserModified = true, want false")
	}
}

// The synthetic sender is not a model here either. It would otherwise
// appear as a row on every per-model panel with no tokens against it.
func TestSyntheticSenderIsNotRecordedOnAnEntry(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	e := oneEntry(t, writeUsageTranscript(t, writeToolUse(ts, "main", "", "<synthetic>")))

	if e.Model != "" {
		t.Errorf("Model = %q, want empty — <synthetic> is a sender, not a model", e.Model)
	}
}

// Non-editing tool calls carry the same context. They are the majority of
// what an agent does, and a per-branch or per-MCP-server view built only
// from edits would describe a small slice of the work.
func TestNonEditingToolCallsCarryTheSameContext(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t, map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
		"gitBranch":            "main",
		"attributionMcpServer": "playwright",
		"message": map[string]any{
			"id": "m1", "model": "claude-opus-5",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "t1",
					"name":  "mcp__playwright__browser_click",
					"input": map[string]any{"ref": "e42"}},
			},
		},
	})

	e := oneEntry(t, path)
	if e.Event != "tool_call" {
		t.Fatalf("Event = %q, want tool_call", e.Event)
	}
	if e.Branch != "main" || e.MCPServer != "playwright" || e.Model != "claude-opus-5" {
		t.Errorf("context missing on a non-editing call: branch=%q mcp=%q model=%q",
			e.Branch, e.MCPServer, e.Model)
	}
}

// toolUseResult carries stdout, stderr, file bodies and structured
// patches. Only the one scalar is lifted out of it, and the rest must
// never reach an entry (NAV-25).
func TestToolUseResultContentDoesNotReachAnEntry(t *testing.T) {
	const secret = "SENTINEL-4a71bc-file-content-must-not-be-stored"

	ts := time.Now().UTC().Add(-time.Hour)
	path := writeUsageTranscript(t,
		writeToolUse(ts, "main", "", "claude-opus-5"),
		map[string]any{
			"type": "user", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
			"toolUseResult": map[string]any{
				"userModified": true,
				"originalFile": secret,
				"content":      secret,
				"structuredPatch": []any{map[string]any{
					"lines": []any{"+ " + secret},
				}},
			},
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1",
					"content": "The file has been updated."},
			}},
		},
	)

	e := oneEntry(t, path)
	rendered, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), secret) {
		t.Errorf("file content reached the entry (NAV-25):\n%s", rendered)
	}
	// Not passing by reading nothing.
	if e.UserModified == nil || !*e.UserModified {
		t.Error("the one safe scalar was not read")
	}
}

func writeToolUse(ts time.Time, branch, mcpServer, model string) map[string]any {
	return map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": "s1", "version": "2.1.0",
		"gitBranch":            branch,
		"attributionMcpServer": mcpServer,
		"message": map[string]any{
			"id": "m1", "model": model,
			"content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "Write",
					"input": map[string]any{
						"file_path": "/repo/x.go", "content": "package x\n",
					}},
			},
		},
	}
}

func oneEntry(t *testing.T, path string) journal.Entry {
	t.Helper()
	entries, err := ParseSince(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	return entries[0]
}
