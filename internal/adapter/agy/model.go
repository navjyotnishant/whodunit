// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Reading the model name out of agy's executor_metadata blob.

package agy

import (
	"database/sql"
	"encoding/binary"
)

// modelPath is where the model name sits inside executor_metadata.data:
// field 10, then 1, then 28.
//
// Determined by walking a real blob rather than guessing. A first attempt
// read field 28 at the top level, on the strength of the bytes
// immediately before "gemini-3.6-flash-high" being e2 01 15 — a field-28
// key and a length. That framing was real but it belonged to an inner
// message, and reading it at the top level found nothing at all. Verified
// against all 8 databases on this machine: every one carries 10.1.28, two
// distinct models between them.
var modelPath = []int{10, 1, 28}

// readModel returns the model this conversation ran on, or "" when the
// database does not say.
//
// **Parsed by field number, deliberately, rather than by searching for a
// string that looks like a model name.** executor_metadata is one blob per
// conversation and it contains the system prompt — instructions about lint
// handling, tool descriptions, the lot. A regex for `gemini-\d` would work
// today and would mean scanning prompt text to find it, which is the thing
// this package must not do (NAV-25). Walking to a known field reads one
// value and never looks at the rest.
//
// A blob that does not parse yields "" rather than an error: agy ships no
// schema for this and the framing may change, in which case the model is
// absent, which is a legitimate answer (NAV-21).
func readModel(db *sql.DB) string {
	var data []byte
	// LIMIT 1 because the table holds one row per conversation and the
	// model is a property of the conversation, not of a step.
	if err := db.QueryRow(`SELECT data FROM executor_metadata LIMIT 1`).Scan(&data); err != nil {
		return ""
	}
	return protoString(data, modelPath)
}

// protoString walks a protobuf message along a path of field numbers and
// returns the value at the end of it.
//
// A minimal walker rather than a generated decoder: agy publishes no
// schema, so there is nothing to generate from, and the alternative — a
// regex over the whole blob — reads prompt text to find its answer.
//
// Unknown fields are skipped by wire type. Anything malformed ends the
// walk and yields "": a partial parse must not return a value from the
// middle of some other field.
func protoString(buf []byte, path []int) string {
	if len(path) == 0 {
		return ""
	}
	field := path[0]
	for i := 0; i < len(buf); {
		key, n := binary.Uvarint(buf[i:])
		if n <= 0 {
			return ""
		}
		i += n

		fieldNum := int(key >> 3)
		switch key & 0x7 {
		case 0: // varint
			_, n := binary.Uvarint(buf[i:])
			if n <= 0 {
				return ""
			}
			i += n
		case 1: // 64-bit
			i += 8
		case 2: // length-delimited
			length, n := binary.Uvarint(buf[i:])
			if n <= 0 {
				return ""
			}
			i += n
			if i+int(length) > len(buf) {
				return ""
			}
			if fieldNum == field {
				if len(path) == 1 {
					return string(buf[i : i+int(length)])
				}
				// Descend. A nested message that does not contain the rest
				// of the path yields "" and the walk continues, because a
				// repeated field can appear more than once and only one
				// occurrence may carry it.
				if v := protoString(buf[i:i+int(length)], path[1:]); v != "" {
					return v
				}
			}
			i += int(length)
		case 5: // 32-bit
			i += 4
		default:
			// Groups (3, 4) are deprecated and not emitted here. An
			// unrecognised wire type means the walk has lost sync, and
			// continuing would return bytes from the middle of a field.
			return ""
		}
		if i > len(buf) {
			return ""
		}
	}
	return ""
}
