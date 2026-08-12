// Package spec implements the AI-Attribution git trailer grammar (SPEC v0.2).
package spec

import (
	"fmt"
	"strconv"
	"strings"
)

// Method is the attribution confidence method, ordered lowest to highest confidence.
type Method string

const (
	MethodUndetermined Method = "undetermined"
	MethodDeclared     Method = "declared"
	MethodInferred     Method = "inferred"
	MethodObserved     Method = "observed"
	MethodIntersected  Method = "intersected"
)

var validMethods = map[Method]bool{
	MethodUndetermined: true,
	MethodDeclared:     true,
	MethodInferred:     true,
	MethodObserved:     true,
	MethodIntersected:  true,
}

// Status is the top-level attribution status.
type Status string

const (
	StatusAssisted     Status = "assisted"
	StatusUndetermined Status = "undetermined"
)

var validStatuses = map[Status]bool{
	StatusAssisted:     true,
	StatusUndetermined: true,
}

// TrailerKey is the git trailer key this spec owns.
const TrailerKey = "AI-Attribution"

// Trailer is a parsed AI-Attribution trailer value.
type Trailer struct {
	Status  Status
	Method  Method
	Agent   string
	Version string
	// Ratio is the share of the commit's changed lines that overlap lines
	// an agent touched — additions and deletions both counted (NAV-8).
	//
	// A pointer because "not computed" and "computed as zero" are different
	// claims: methods with no line-level evidence (declared, inferred) can
	// never have one, and emitting 0.00 there would assert the agent
	// contributed nothing.
	Ratio   *float64
	Session string
	Extra   map[string]string // unknown keys, preserved verbatim per spec
}

// Undetermined is the trailer stamped when no determination could be made.
// Absence must never mean none (NAV-21): every commit gets a trailer.
func Undetermined() Trailer {
	return Trailer{Status: StatusUndetermined, Method: MethodUndetermined}
}

// Format renders a Trailer as "AI-Attribution: key=value; key=value".
func (t Trailer) Format() string {
	var b strings.Builder
	b.WriteString(TrailerKey)
	b.WriteString(": status=")
	b.WriteString(string(t.Status))
	b.WriteString("; method=")
	b.WriteString(string(t.Method))
	if t.Agent != "" {
		fmt.Fprintf(&b, "; agent=%s", t.Agent)
	}
	if t.Version != "" {
		fmt.Fprintf(&b, "; agent_version=%s", t.Version)
	}
	// ratio is emitted only when it was actually computed. A method with no
	// line-level evidence (declared, inferred) has nothing to compute it
	// from, and an unknown ratio must be absent rather than reported as
	// 0.00 — a fabricated zero reads as "the agent contributed nothing".
	if t.Ratio != nil {
		fmt.Fprintf(&b, "; ratio=%.2f", *t.Ratio)
	}
	if t.Session != "" {
		fmt.Fprintf(&b, "; session=%s", t.Session)
	}
	for k, v := range t.Extra {
		fmt.Fprintf(&b, "; %s=%s", k, v)
	}
	return b.String()
}

// Parse parses a trailer value (the part after "AI-Attribution: ").
// Returns an error for any malformed, missing-required-key, or invalid-value trailer.
func Parse(value string) (Trailer, error) {
	t := Trailer{Extra: map[string]string{}}
	fields := strings.Split(value, ";")

	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			return Trailer{}, fmt.Errorf("spec: malformed field %q", f)
		}
		key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		if !isToken(val) {
			return Trailer{}, fmt.Errorf("spec: value for %q is not a valid token: %q", key, val)
		}

		switch key {
		case "status":
			if !validStatuses[Status(val)] {
				return Trailer{}, fmt.Errorf("spec: invalid status %q", val)
			}
			t.Status = Status(val)
		case "method":
			if !validMethods[Method(val)] {
				return Trailer{}, fmt.Errorf("spec: invalid method %q", val)
			}
			t.Method = Method(val)
		case "agent":
			t.Agent = val
		case "agent_version":
			t.Version = val
		case "session":
			t.Session = val
		case "ratio":
			r, err := strconv.ParseFloat(val, 64)
			if err != nil || r < 0 || r > 1 {
				return Trailer{}, fmt.Errorf("spec: invalid ratio %q", val)
			}
			t.Ratio = &r
		default:
			t.Extra[key] = val
		}
	}

	if t.Status == "" || t.Method == "" {
		return Trailer{}, fmt.Errorf("spec: trailer missing required key status or method")
	}
	return t, nil
}

// isToken reports whether s contains only characters legal in a trailer token value.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// Explain returns a plain-English gloss for a method.
//
// The method names are spec vocabulary: they appear in the trailer, in the
// dashboards, and in every commit already stamped, so they cannot be
// renamed to something friendlier without either breaking that or keeping
// two names for one concept. What they can do is explain themselves at the
// point they are displayed.
//
// Written from the reader's side — what the evidence shows — rather than
// from the collector's. "The agent's exact lines are in this commit" tells
// someone what they can conclude; "line hashes intersected" does not.
func (m Method) Explain() string {
	switch m {
	case MethodIntersected:
		return "the agent's exact lines are in the commit"
	case MethodObserved:
		return "the agent edited these files"
	case MethodInferred:
		return "inferred from surrounding evidence"
	case MethodDeclared:
		return "the author declared it, unverified"
	case MethodUndetermined:
		return "no evidence either way"
	default:
		return ""
	}
}
