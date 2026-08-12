package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClassifyResult(t *testing.T) {
	cases := []struct {
		name    string
		isError bool
		text    string
		want    Outcome
	}{
		{"success", false, "", OutcomeAccepted},
		{"success with output", false, "File written", OutcomeAccepted},
		{"user declined", true, "The user doesn't want to proceed with this tool use. The tool use was rejected", OutcomeRejected},
		{"user rejected variant", true, "Tool use was rejected by the user", OutcomeRejected},
		{"edit failed", true, "String to replace not found in file", OutcomeFailed},
		{"command failed", true, "Exit code 1\ngo: build failed", OutcomeFailed},
	}
	for _, c := range cases {
		if got := classifyResult(c.isError, c.text); got != c.want {
			t.Errorf("%s: classifyResult = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRejectedIsNotConfusedWithFailed(t *testing.T) {
	// A person declining and a tool erroring are different events. Merging
	// them would make an acceptance rate meaningless — a broken build would
	// look like a developer rejecting the agent's work.
	if classifyResult(true, "Exit code 1") == OutcomeRejected {
		t.Error("a failing command was counted as a rejection")
	}
	if classifyResult(true, "The user doesn't want to proceed") == OutcomeFailed {
		t.Error("a user rejection was counted as a failure")
	}
}

// writeTranscript builds a transcript with one accepted and one rejected
// Write, so the join from call to result is exercised rather than assumed.
func writeTranscript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	lines := []string{
		`{"type":"assistant","timestamp":"2026-08-12T10:00:00Z","sessionId":"s1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"call_ok","name":"Write","input":{"file_path":"/repo/kept.go","content":"package kept\nfunc Kept() {}\n"}}]}}`,
		`{"type":"user","timestamp":"2026-08-12T10:00:01Z","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":"call_ok","content":"File created"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-12T10:01:00Z","sessionId":"s1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"call_no","name":"Write","input":{"file_path":"/repo/declined.go","content":"package declined\nfunc Declined() {}\n"}}]}}`,
		`{"type":"user","timestamp":"2026-08-12T10:01:01Z","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":"call_no","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-12T10:02:00Z","sessionId":"s1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"call_err","name":"Edit","input":{"file_path":"/repo/kept.go","old_string":"missing","new_string":"replacement"}}]}}`,
		`{"type":"user","timestamp":"2026-08-12T10:02:01Z","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":"call_err","is_error":true,"content":[{"type":"text","text":"String to replace not found in file"}]}]}}`,
	}
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func TestParseSinceRecordsOutcomes(t *testing.T) {
	entries, err := ParseSince(writeTranscript(t), time.Time{})
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}

	byFile := map[string]string{}
	for _, e := range entries {
		byFile[e.File] = e.Outcome
	}
	if byFile["/repo/kept.go"] == "" {
		t.Fatal("no entry for the accepted write")
	}
	if got := byFile["/repo/declined.go"]; got != string(OutcomeRejected) {
		t.Errorf("declined write outcome = %q, want rejected", got)
	}
}

func TestRejectedEditContributesNoLines(t *testing.T) {
	// A rejected call never reached the file, so counting its text as
	// agent-authored would attribute lines that do not exist.
	entries, err := ParseSince(writeTranscript(t), time.Time{})
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	for _, e := range entries {
		if e.Outcome == string(OutcomeAccepted) {
			continue
		}
		if len(e.LineHashes) != 0 {
			t.Errorf("%s (%s) carried %d line hashes; only accepted calls reached the file",
				e.File, e.Outcome, len(e.LineHashes))
		}
		if e.LinesAdded != 0 || e.LinesRemoved != 0 {
			t.Errorf("%s (%s) reported %d/%d lines; it never reached the file",
				e.File, e.Outcome, e.LinesAdded, e.LinesRemoved)
		}
	}
}

func TestMissingResultIsUnknownNotAccepted(t *testing.T) {
	// A truncated or in-flight transcript must not have its unfinished
	// calls counted as accepted — that would flatter the rate.
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	line := `{"type":"assistant","timestamp":"2026-08-12T10:00:00Z","sessionId":"s1","version":"2.1.227","message":{"content":[{"type":"tool_use","id":"orphan","name":"Write","input":{"file_path":"/repo/x.go","content":"package x\n"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := ParseSince(path, time.Time{})
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Outcome != string(OutcomeUnknown) {
		t.Errorf("outcome = %q, want unknown for a call with no result", entries[0].Outcome)
	}
}

func TestParseSessionActivity(t *testing.T) {
	acts, err := ParseSessionActivity(writeTranscript(t), time.Time{})
	if err != nil {
		t.Fatalf("ParseSessionActivity: %v", err)
	}
	if len(acts) != 1 {
		t.Fatalf("want 1 session, got %d", len(acts))
	}
	a := acts[0]
	if a.AgentMessages != 3 {
		t.Errorf("AgentMessages = %d, want 3", a.AgentMessages)
	}
	if a.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", a.ToolCalls)
	}
	if a.DistinctTools != 2 {
		t.Errorf("DistinctTools = %d, want 2 (Write and Edit)", a.DistinctTools)
	}
	// Every user record here carries only tool results, which is the
	// harness replying — not a person typing.
	if a.UserMessages != 0 {
		t.Errorf("UserMessages = %d, want 0: tool-result records are not human messages", a.UserMessages)
	}
}

func TestSessionActivityRecordsNoContent(t *testing.T) {
	// NAV-55 is counts only. Nothing derived from what was written may
	// appear on the record.
	acts, err := ParseSessionActivity(writeTranscript(t), time.Time{})
	if err != nil {
		t.Fatalf("ParseSessionActivity: %v", err)
	}
	a := acts[0]
	for field, value := range map[string]string{
		"Session": a.Session, "Agent": a.Agent, "AgentVersion": a.AgentVersion,
	} {
		if containsAny(value, "package", "func", "declined", "proceed") {
			t.Errorf("%s carries transcript content: %q", field, value)
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
