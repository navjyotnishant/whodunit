// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The pre-push hook must publish when configured, stay silent
// when not, and never block a push.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navjyotnishant/whodunit/internal/config"
)

// NAV-72 criterion 7. A hook that prints on every push for a feature
// nobody configured teaches people to ignore its output — including the
// warning in criterion 8 that they would actually want to read.
func TestPrePushSaysNothingWhenUnconfigured(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	var out bytes.Buffer
	if err := runPrePush(&out); err != nil {
		t.Fatalf("runPrePush returned %v; it must never fail a push", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unconfigured pre-push printed %q; it must be silent", out.String())
	}
}

// NAV-72 criterion 8, and the one that matters most. A failed push is a
// blocked deploy, and a tool that can block a deploy gets uninstalled.
func TestPrePushNeverBlocksThePush(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_SYNC_PASSWORD", "irrelevant")

	// A target that cannot possibly be reached.
	writeSyncConfig(t, home, &config.SyncConfig{
		DSN:         "mysql://dun@127.0.0.1:1/lake",
		PasswordEnv: "WHODUNIT_SYNC_PASSWORD",
		OnPush:      true,
	})

	var out bytes.Buffer
	if err := runPrePush(&out); err != nil {
		t.Fatalf("runPrePush returned %v with an unreachable database; "+
			"a push must succeed regardless", err)
	}

	s := out.String()
	if !strings.Contains(s, "whodunit:") {
		t.Errorf("the failure was not attributed to whodunit, so it reads "+
			"like a git error:\n%s", s)
	}
	if !strings.Contains(s, "recorded locally") {
		t.Errorf("the failure did not say the work is still recorded; that "+
			"is what makes it ignorable rather than alarming:\n%s", s)
	}
}

// A password variable that is named but unset is a misconfiguration worth
// stating, not a connection error to puzzle over.
func TestPrePushReportsAnUnsetPasswordVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_SYNC_PASSWORD", "")

	writeSyncConfig(t, home, &config.SyncConfig{
		DSN:         "mysql://dun@localhost:3306/lake",
		PasswordEnv: "WHODUNIT_SYNC_PASSWORD",
		OnPush:      true,
	})

	var out bytes.Buffer
	if err := runPrePush(&out); err != nil {
		t.Fatalf("runPrePush returned %v", err)
	}
	if !strings.Contains(out.String(), "WHODUNIT_SYNC_PASSWORD") {
		t.Errorf("the message does not name the variable that is unset:\n%s", out.String())
	}
}

// on_push false means configured-but-manual: `dun sync` still works, the
// hook does not fire.
func TestPrePushRespectsOnPushFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	writeSyncConfig(t, home, &config.SyncConfig{
		DSN:         "mysql://dun@127.0.0.1:1/lake",
		PasswordEnv: "",
		OnPush:      false,
	})

	var out bytes.Buffer
	if err := runPrePush(&out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("on_push=false still printed %q", out.String())
	}
}

// NAV-72 criterion 14. The password lives in an environment variable; the
// config file records only its name.
func TestPasswordIsNeverWrittenToDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	t.Setenv("WHODUNIT_SYNC_PASSWORD", "hunter2-the-real-secret")

	writeSyncConfig(t, home, &config.SyncConfig{
		DSN:         "mysql://dun@lake.internal:3306/lake",
		PasswordEnv: "WHODUNIT_SYNC_PASSWORD",
		OnPush:      true,
	})

	data, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2-the-real-secret") {
		t.Fatalf("the password was written to config.json:\n%s", data)
	}
	if !strings.Contains(string(data), "WHODUNIT_SYNC_PASSWORD") {
		t.Fatalf("the config does not name the password variable:\n%s", data)
	}
}

// Resolve fills the password in from the environment; Redacted must not
// leak it back out into terminal scrollback or a screenshot.
func TestResolveAndRedact(t *testing.T) {
	t.Setenv("WHODUNIT_SYNC_PASSWORD", "s3cret")
	s := &config.SyncConfig{
		DSN:         "mysql://dun@lake.internal:3306/lake",
		PasswordEnv: "WHODUNIT_SYNC_PASSWORD",
	}

	resolved, err := s.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resolved, "s3cret") {
		t.Fatalf("Resolve did not inject the password: %s", resolved)
	}
	if !strings.Contains(resolved, "lake.internal:3306") {
		t.Fatalf("Resolve mangled the host: %s", resolved)
	}

	if got := s.Redacted(); strings.Contains(got, "s3cret") {
		t.Fatalf("Redacted leaked the password: %s", got)
	}
}

// A target needing no password at all is legitimate — a local database, or
// a DSN carrying its own credentials.
func TestResolveWithoutAPasswordVariable(t *testing.T) {
	s := &config.SyncConfig{DSN: "mysql://root@localhost:3306/lake"}
	got, err := s.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != s.DSN {
		t.Fatalf("Resolve changed a passwordless DSN: %s", got)
	}
}

func writeSyncConfig(tb testing.TB, home string, sync *config.SyncConfig) {
	tb.Helper()
	cfg, err := config.Load()
	if err != nil {
		tb.Fatal(err)
	}
	cfg.Sync = sync
	if err := config.Save(cfg); err != nil {
		tb.Fatal(err)
	}
}
