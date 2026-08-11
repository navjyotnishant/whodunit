// Package config reads global, cross-repo Whodunit settings from ~/.whodunit.
//
// Per-repo state (the journal) stays under that repo's .git dir — it belongs
// to the repo. This package is for settings that don't belong to any one
// repo: subscription spend for cost-per-line, and later the daemon's
// multi-repo registry.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the global settings file at ~/.whodunit/config.json.
type Config struct {
	// MonthlySpend is the AI agent subscription cost per month, in the
	// smallest currency unit the user reports in (e.g. dollars). Used to
	// compute cost-per-attributed-line; zero means "not configured".
	MonthlySpend float64 `json:"monthly_spend,omitempty"`

	// RetentionDays is the default journal retention window (NAV-33).
	RetentionDays int `json:"retention_days,omitempty"`
}

const defaultRetentionDays = 14

// Dir returns ~/.whodunit, honoring WHODUNIT_HOME for tests and overrides.
func Dir() (string, error) {
	if d := os.Getenv("WHODUNIT_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".whodunit"), nil
}

// Load reads the global config, returning defaults if it doesn't exist yet.
func Load() (Config, error) {
	cfg := Config{RetentionDays: defaultRetentionDays}

	dir, err := Dir()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = defaultRetentionDays
	}
	return cfg, nil
}

// Save writes the global config, creating ~/.whodunit if needed.
func Save(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}
