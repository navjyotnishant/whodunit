package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIngestCommandOnRepoWithNoClaudeCodeSessions(t *testing.T) {
	// A repo whose cwd has never had a Claude Code session — SessionFiles
	// returns empty, ingest should report 0 events, not error.
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"ingest"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ingest with no sessions = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "ingested 0 event(s) from 0 session file(s)") {
		t.Errorf("output = %q, want it to report 0 events from 0 sessions", buf.String())
	}
}

func TestIngestCommandRejectsInvalidSince(t *testing.T) {
	chdirToTestRepo(t)

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"ingest", "--since", "not-a-timestamp"})

	if err := cmd.Execute(); err == nil {
		t.Error("ingest --since not-a-timestamp = nil error, want error")
	}
}
