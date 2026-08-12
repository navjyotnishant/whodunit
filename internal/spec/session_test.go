package spec

import (
	"strings"
	"testing"
)

// The whole point: the agent's own id must not survive into the token.
// On Claude Code that id is the transcript filename, so a commit carrying
// it points at a file holding every prompt of the session.
func TestTokenDoesNotContainTheRawSession(t *testing.T) {
	raw := "57113870-ecd7-4b29-8957-18ec3e564d3b"
	got := SessionToken("repo-abc", raw)

	if strings.Contains(got, raw) {
		t.Fatalf("token %q contains the raw session id", got)
	}
	// Not even a fragment: the first segment of a UUID is enough to find a
	// transcript by prefix.
	if strings.Contains(got, "57113870") {
		t.Fatalf("token %q leaks a prefix of the raw session id", got)
	}
}

// Commits from one session must share a token, or the trailer loses the
// only property it needs this field for.
func TestSameSessionSameToken(t *testing.T) {
	a := SessionToken("repo-abc", "session-1")
	b := SessionToken("repo-abc", "session-1")
	if a != b {
		t.Fatalf("same session produced %q and %q", a, b)
	}
}

func TestDifferentSessionsDifferentTokens(t *testing.T) {
	a := SessionToken("repo-abc", "session-1")
	b := SessionToken("repo-abc", "session-2")
	if a == b {
		t.Fatalf("two sessions produced the same token %q", a)
	}
}

// Repo-scoping is what stops someone reading two repositories' commits and
// correlating a person's working periods across them.
func TestSameSessionInDifferentReposIsNotCorrelatable(t *testing.T) {
	a := SessionToken("repo-abc", "session-1")
	b := SessionToken("repo-xyz", "session-1")
	if a == b {
		t.Fatalf("the same session yielded token %q in both repositories; "+
			"a reader could link the two repositories' commits", a)
	}
}

// An empty session stays empty rather than becoming the hash of nothing,
// which would look like real evidence.
func TestEmptySessionYieldsEmptyToken(t *testing.T) {
	if got := SessionToken("repo-abc", ""); got != "" {
		t.Fatalf("empty session produced token %q", got)
	}
}

func TestTokenLength(t *testing.T) {
	got := SessionToken("repo-abc", "session-1")
	if len(got) != SessionTokenLength {
		t.Fatalf("token %q is %d chars, want %d", got, len(got), SessionTokenLength)
	}
	for _, r := range got {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("token %q is not lowercase hex", got)
		}
	}
}

// A token has to survive a round trip through the trailer format, or the
// grouping it exists for breaks on parse.
func TestTokenSurvivesTrailerRoundTrip(t *testing.T) {
	token := SessionToken("repo-abc", "session-1")
	formatted := Trailer{
		Status:  StatusAssisted,
		Method:  MethodObserved,
		Agent:   "claude-code",
		Session: token,
	}.Format()

	const prefix = TrailerKey + ": "
	parsed, err := Parse(strings.TrimPrefix(formatted, prefix))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Session != token {
		t.Fatalf("session round-tripped as %q, want %q", parsed.Session, token)
	}
}

// Without a repo id the token is weaker than intended but must still be a
// hash: an unscoped token is a smaller problem than a leaked filename.
func TestTokenWithoutARepoIDIsStillAHash(t *testing.T) {
	raw := "57113870-ecd7-4b29-8957-18ec3e564d3b"
	got := SessionToken("", raw)
	if got == "" || strings.Contains(got, raw) {
		t.Fatalf("token %q with no repo id did not hash the session", got)
	}
	if len(got) != SessionTokenLength {
		t.Fatalf("token %q is the wrong length", got)
	}
}
