package config

import "testing"

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
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}
