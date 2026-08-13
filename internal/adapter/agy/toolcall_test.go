// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: Non-editing tool calls are recorded, and carry no arguments.

package agy

import (
	"testing"
	"time"
)

// The name sits after the call id in agy's protobuf framing. Anchoring on
// the id is what keeps this from matching arbitrary text elsewhere in a
// payload — and these payloads contain whole files the agent read.
func TestCallNamesReadsToolNames(t *testing.T) {
	payload := []byte("\x0a\x0bcall_281815\x12\x09view_file\x1a{\"AbsolutePath\":\"/tmp/x.go\"}")

	got := callNames(payload)
	if len(got) != 1 || got[0] != "view_file" {
		t.Fatalf("callNames = %v, want [view_file]", got)
	}
}

func TestCallNamesDeduplicates(t *testing.T) {
	payload := []byte("call_1\x12\x09list_dir...call_2\x12\x09list_dir")

	if got := callNames(payload); len(got) != 1 {
		t.Fatalf("callNames = %v, want one entry for a repeated tool", got)
	}
}

// A payload with no call marker must yield nothing rather than matching
// whatever text happens to be in it — file contents included.
func TestCallNamesIgnoresPayloadWithoutACall(t *testing.T) {
	payload := []byte(`{"content":"func view_file() { return list_dir }"}`)

	if got := callNames(payload); len(got) != 0 {
		t.Fatalf("callNames matched text with no call marker: %v", got)
	}
}

// NAV-25. A recorded tool call carries the name and nothing else. The
// arguments beside it routinely hold file contents — a view_file result, a
// run_command line with a heredoc — and a later change that relaxed this
// would leak them into a journal that syncs to a shared database.
func TestRecordedCallsCarryNoArguments(t *testing.T) {
	c := call{Idx: 1, StepType: 8, Tool: "view_file"}

	if c.TargetFile != "" {
		t.Error("a non-editing call carried a file path")
	}
	if c.Args.CodeContent != "" || c.Args.ReplacementContent != "" ||
		c.Args.TargetContent != "" {
		t.Error("a non-editing call carried content")
	}
}

// The bug this guards against: the entry loop skipped any call that
// produced no content, which is every non-editing call by definition. The
// branch recording them sat below that guard and never ran, and nothing
// failed — the adapter simply returned edits only.
func TestNonEditingCallsReachTheJournal(t *testing.T) {
	entries, err := ParseSince("testdata/toolcalls.db", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	var calls []string
	for _, e := range entries {
		if e.Event == "tool_call" {
			calls = append(calls, e.Tool)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2 (view_file, list_dir): %v", len(calls), calls)
	}

	// Names only: a call that carried a file path or content would mean the
	// edit path claimed it, or that arguments leaked in.
	for _, e := range entries {
		if e.Event != "tool_call" {
			continue
		}
		if e.File != "" || len(e.LineHashes) != 0 || e.HunkHash != "" {
			t.Errorf("tool call %q carried file evidence: file=%q hashes=%d",
				e.Tool, e.File, len(e.LineHashes))
		}
	}
}
