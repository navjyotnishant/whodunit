package claudecode

import "strings"

// Outcome is what happened to a tool call after the agent proposed it
// (NAV-54).
//
// A vendor's usage API reports an acceptance rate because it sees both
// sides of the decision. So do we: a declined call leaves an explicit
// marker in the session transcript, distinguishable from one that failed
// for a technical reason.
type Outcome string

const (
	// OutcomeAccepted means the call ran and produced no error. For an Edit
	// or Write, the change reached the file.
	OutcomeAccepted Outcome = "accepted"

	// OutcomeRejected means a human declined it — the permission prompt was
	// answered no, or a hook blocked it. This is the signal an acceptance
	// rate needs.
	OutcomeRejected Outcome = "rejected"

	// OutcomeFailed means it ran and errored: a string that no longer
	// matched, a file changed underneath, a command that exited non-zero.
	// Kept separate from rejected because a tool failing is not a person
	// declining, and merging them would make an acceptance rate meaningless.
	OutcomeFailed Outcome = "failed"

	// OutcomeUnknown means no result was found for the call. Recorded
	// honestly rather than assumed accepted — the transcript may be
	// truncated, or the session may still be in flight.
	OutcomeUnknown Outcome = "unknown"
)

// rejectionMarkers are the phrases Claude Code uses when a human declines a
// tool call.
//
// Detecting this by matching prose is fragile, and the failure mode is
// silent and flattering: every rejection quietly becomes an acceptance and
// the rate climbs to 100%. The golden fixtures (NAV-30) include a rejected
// call so a wording change fails a test rather than inflating a metric.
var rejectionMarkers = []string{
	"user doesn't want to proceed",
	"user rejected",
	"tool use was rejected",
	"operation was rejected by the user",
}

// classifyResult determines what happened to a tool call from its result.
func classifyResult(isError bool, text string) Outcome {
	if !isError {
		return OutcomeAccepted
	}
	lower := strings.ToLower(text)
	for _, marker := range rejectionMarkers {
		if strings.Contains(lower, marker) {
			return OutcomeRejected
		}
	}
	return OutcomeFailed
}

// IsRejectionText reports whether a tool result's text marks a human
// declining. Exported so the drift fixtures can assert the markers still
// match real transcripts.
func IsRejectionText(text string) bool {
	return classifyResult(true, text) == OutcomeRejected
}
