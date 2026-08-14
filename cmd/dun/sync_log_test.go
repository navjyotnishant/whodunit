// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: What sync writes to the log, and what it must never write.

package main

import (
	"strings"
	"testing"
)

// The log is read by people and pasted into issue reports. A resolved DSN
// carries the sync password, so anything naming the target has to redact it
// — config.SyncConfig.Redacted covers a configured target, but --to supplies
// a raw string that has never been through it.
func TestTheSyncTargetIsLoggedWithoutItsPassword(t *testing.T) {
	const secret = "hunter2-should-never-appear"
	got := redactedTarget("mysql://merico:" + secret + "@db.example.com:3306/lake")

	if strings.Contains(got, secret) {
		t.Fatalf("the password reached the log: %q", got)
	}
	// The rest has to survive, or the entry cannot tell two targets apart.
	for _, want := range []string{"merico", "db.example.com", "lake"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction removed %q, leaving %q — the entry no longer "+
				"identifies which target was used", want, got)
		}
	}
}

// A malformed DSN must not fall through to printing itself, which would
// defeat the redaction above.
func TestAnUnparseableTargetIsNotEchoed(t *testing.T) {
	got := redactedTarget("://not a url:secret@@@")
	if strings.Contains(got, "secret") {
		t.Errorf("an unparseable DSN was echoed into the log: %q", got)
	}
}
