package config

import (
	"reflect"
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

	want := Config{MonthlySpend: 20, RetentionDays: 30}
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
