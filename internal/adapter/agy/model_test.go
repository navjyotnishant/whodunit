package agy

import (
	"strings"
	"testing"
	"time"
)

// protoString walks to a nested field rather than searching for a string
// that looks like a model name.
//
// The distinction is a privacy one, not a style one: executor_metadata is
// one blob per conversation carrying the system prompt — tool
// descriptions, lint-handling instructions, the lot. A regex for
// `gemini-\d` would find the answer by reading all of that, which is
// exactly what this package must not do (NAV-25).
func TestProtoStringWalksToANestedField(t *testing.T) {
	// field 10 { field 1 { field 28: "gemini-3.7-flash-high" } }
	inner := protoField(28, []byte("gemini-3.7-flash-high"))
	middle := protoField(1, inner)
	outer := protoField(10, middle)

	if got := protoString(outer, []int{10, 1, 28}); got != "gemini-3.7-flash-high" {
		t.Errorf("protoString = %q, want gemini-3.7-flash-high", got)
	}
}

// The path is 10.1.28, not 28 at the top level.
//
// A first implementation read field 28 directly, on the strength of the
// bytes before the model string being a field-28 key. The framing was
// real but belonged to an inner message, so reading it at the top level
// found nothing — and would have shipped as "agy reports no model".
func TestTheModelIsNotATopLevelField(t *testing.T) {
	inner := protoField(28, []byte("gemini-3.7-flash-high"))
	middle := protoField(1, inner)
	outer := protoField(10, middle)

	if got := protoString(outer, []int{28}); got != "" {
		t.Errorf("field 28 at the top level returned %q; it is nested at 10.1.28 "+
			"and a top-level read finds nothing", got)
	}
}

// Unrelated fields are skipped by wire type rather than confusing the
// walk. A blob carries varints, fixed-width values and other messages
// before the one being looked for.
func TestProtoStringSkipsUnrelatedFields(t *testing.T) {
	var buf []byte
	buf = append(buf, protoVarint(3, 12345)...)            // a varint field
	buf = append(buf, protoField(7, []byte("ignored"))...) // another string
	buf = append(buf, protoFixed64(9)...)                  // a fixed-width field
	buf = append(buf, protoField(10, protoField(1, protoField(28,
		[]byte("gemini-3.6-flash-high"))))...)

	if got := protoString(buf, []int{10, 1, 28}); got != "gemini-3.6-flash-high" {
		t.Errorf("protoString = %q; unrelated fields broke the walk", got)
	}
}

// A malformed blob yields "" rather than a value from the middle of some
// other field. agy ships no schema, so the framing can change and the
// honest answer to "what model" is then "unknown" (NAV-21).
func TestMalformedBlobYieldsNothing(t *testing.T) {
	for name, buf := range map[string][]byte{
		"empty":            {},
		"truncated length": {0x52, 0xff},
		"length past end":  {0x52, 0x40, 0x01, 0x02},
		"garbage":          []byte("not protobuf at all"),
	} {
		if got := protoString(buf, []int{10, 1, 28}); got != "" {
			t.Errorf("%s: protoString = %q, want empty", name, got)
		}
	}
}

// Every entry from a conversation carries the model, including
// non-editing tool calls — which are the majority of what an agent does.
//
// This is the site that was missed first time round: the model reached the
// call struct but only the edit-producing entry read it, so 139 of 149
// entries came back empty while the extraction itself was working.
func TestEveryEntryCarriesTheModel(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	// The fixture predates model recording, so the assertion is that the
	// field is consistent across entries rather than a specific value —
	// whatever readModel returns must reach all of them.
	want := entries[0].Model
	for _, e := range entries {
		if e.Model != want {
			t.Errorf("%s has model %q, want %q — one entry site is not reading it",
				e.File, e.Model, want)
		}
	}
}

// agy records no branch and no MCP server, and both are verified absent
// rather than merely unread.
//
// Branch: the gitBranchName strings in these databases are Linear's
// SUGGESTED branch inside MCP responses, not the checked-out one. Storing
// that would be confidently wrong, which is worse than empty.
//
// MCP server: steps.permissions was expected to carry it and does not —
// it holds the command text being approved, including whole shell
// scripts. That is RISKY content under NAV-25, not an identifier.
func TestAgyReportsNoBranchOrMCPServer(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Branch != "" {
			t.Errorf("%s has branch %q; agy records none, and the gitBranchName "+
				"strings in its databases are Linear's suggested branch inside "+
				"MCP responses rather than the checked-out one", e.File, e.Branch)
		}
		if e.MCPServer != "" {
			t.Errorf("%s has mcp_server %q; steps.permissions holds the command "+
				"text being approved, not a server identity", e.File, e.MCPServer)
		}
		if e.UserModified != nil {
			t.Errorf("%s has user_modified set; agy has no such signal", e.File)
		}
	}
}

// The model must not be a fragment of the system prompt. If the walk ever
// loses sync it would return arbitrary bytes, and a model name is short
// and has a recognisable shape.
func TestTheModelLooksLikeAModelName(t *testing.T) {
	entries, err := ParseSince(fixture, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Model == "" {
			continue
		}
		if len(e.Model) > 64 {
			t.Errorf("model is %d characters — the walk lost sync and returned "+
				"prompt text (NAV-25): %.60q", len(e.Model), e.Model)
		}
		if strings.ContainsAny(e.Model, " \n\t") {
			t.Errorf("model %q contains whitespace; a model name does not", e.Model)
		}
	}
}

// protoField encodes a length-delimited protobuf field.
func protoField(num int, value []byte) []byte {
	out := appendVarint(nil, uint64(num)<<3|2)
	out = appendVarint(out, uint64(len(value)))
	return append(out, value...)
}

func protoVarint(num int, value uint64) []byte {
	out := appendVarint(nil, uint64(num)<<3|0)
	return appendVarint(out, value)
}

func protoFixed64(num int) []byte {
	out := appendVarint(nil, uint64(num)<<3|1)
	return append(out, make([]byte, 8)...)
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
