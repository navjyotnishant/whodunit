package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RetentionDays != defaultRetentionDays {
		t.Errorf("RetentionDays = %d, want default %d", cfg.RetentionDays, defaultRetentionDays)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	// Every defaulted field is set explicitly. Load fills a zero value with
	// its default, so a round-trip of a partially-filled Config compares
	// unequal to what was saved — which is correct behaviour, not a bug to
	// assert against.
	want := Config{MonthlySpend: 20, RetentionDays: 30, BackupDays: defaultBackupDays}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// reflect.DeepEqual rather than ==: Config carries a map now, and a
	// struct containing one is not comparable.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

// Per-agent paths survive a save/load round trip, since a path that does
// not persist is the same as no override at all.
func TestAgentPathRoundTrip(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	want := Config{
		RetentionDays: defaultRetentionDays,
		Agents: map[string]AgentConfig{
			"claude-code": {Path: "/somewhere/else"},
		},
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Agents["claude-code"].Path != "/somewhere/else" {
		t.Fatalf("agent path did not survive: %+v", got.Agents)
	}
}

// Redacted must never return a password, including when it cannot parse.
//
// The fallback used to be `return s.DSN` — the raw string, password and
// all — and runConfigList prints the result straight to the terminal. A DSN
// that url.Parse rejects is exactly the case where a user is likely to be
// looking at `dun config` to find out what is wrong, so the failure path
// was the one most likely to be on screen.
func TestRedactedNeverPrintsAPassword(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
	}{
		{"ordinary", "mysql://user:hunter2@host:3306/lake"},
		{"unparseable", "mysql://user:hunter2@host:not-a-port/lake"},
		{"control characters", "mysql://user:hunter2@ho\x7fst/lake"},
		{"password in a query parameter", "mysql://user@host/lake?password=hunter2"},
		{"no password at all", "mysql://user@host:3306/lake"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &SyncConfig{DSN: c.dsn}
			got := s.Redacted()
			if strings.Contains(got, "hunter2") {
				t.Errorf("Redacted() = %q, which contains the password. This "+
					"is printed by `dun config` into terminal scrollback.", got)
			}
			if got == "" {
				t.Error("Redacted() returned nothing; it has to be printable")
			}
		})
	}
}

// Retention must never fall inside the hook's lookback window.
//
// The hook queries line hashes for the last LookbackWindow days; pruning
// them sooner deletes evidence a commit was about to match, silently
// downgrading intersected to observed. Nobody would see it: the trailer is
// still written, it just claims less than it should.
//
// config cannot import attribution without a cycle, so the number is typed
// twice and this asserts the relationship instead. Written as a plain
// arithmetic check rather than an import so the constraint is visible in
// the package that owns the value.
func TestRetentionCannotPruneInsideTheHookLookback(t *testing.T) {
	// attribution.LookbackWindow, in days. Duplicated deliberately — see
	// MinRetentionDays' own comment on why the packages cannot import each
	// other — which is exactly why it needs asserting.
	const hookLookbackDays = 30

	if MinRetentionDays < hookLookbackDays {
		t.Fatalf("MinRetentionDays is %d but the hook looks back %d days: "+
			"pruning would delete hashes the hook still queries, turning "+
			"intersected commits into observed ones with no error anywhere",
			MinRetentionDays, hookLookbackDays)
	}
	if defaultRetentionDays < MinRetentionDays {
		t.Fatalf("the default retention (%d) is below the minimum (%d)",
			defaultRetentionDays, MinRetentionDays)
	}
}

// A retention below the minimum must be clamped however it got there.
//
// Validating only in `dun config set` left the file as a way around it: a
// value written before the minimum existed, or edited by hand, would prune
// inside the hook's window.
func TestLoadClampsRetentionBelowTheMinimum(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)

	if err := os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"retention_days": 3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetentionDays < MinRetentionDays {
		t.Errorf("Load returned retention_days = %d from a hand-edited "+
			"config; it must be clamped to at least %d",
			cfg.RetentionDays, MinRetentionDays)
	}
}
