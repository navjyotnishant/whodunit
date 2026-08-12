// Package config owns the layout of ~/.whodunit and the global settings
// stored there.
//
// Everything whodunit records lives under a single home directory, split by
// kind:
//
//	~/.whodunit/config.json          settings you might edit or back up
//	~/.whodunit/data/journal.db      observations, regenerable via `dun ingest`
//	~/.whodunit/baselines/<id>.json  pre-adoption snapshots, NOT regenerable
//
// One self-contained home is deliberate. A tool whose pitch is that you can
// audit and remove everything it recorded should have exactly one place to
// look and one thing to delete. XDG's split across config/data/state is
// honored only when XDG_DATA_HOME is explicitly set, so Linux users who
// want it get it and nobody else has to hunt across three directories.
package config

import (
	"encoding/json"
	"fmt"
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

// dirPerm is owner-only. The journal records which files you edited and
// when, which is nobody else's business on a shared machine.
const dirPerm = 0o700

// filePerm is owner-only, for the same reason.
const filePerm = 0o600

// Dir returns the whodunit home directory, honoring WHODUNIT_HOME for tests
// and overrides.
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

// DataDir returns the directory holding regenerable data — today, the
// journal database. Honors XDG_DATA_HOME when explicitly set.
func DataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" && os.Getenv("WHODUNIT_HOME") == "" {
		return filepath.Join(xdg, "whodunit"), nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data"), nil
}

// BaselinesDir returns the directory holding baseline snapshots. These are
// kept apart from data/ because they are not regenerable: a baseline
// measures a window that closes permanently once hooks are installed.
func BaselinesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "baselines"), nil
}

// EnsureDir creates dir with owner-only permissions, and repairs the
// permissions if it already exists more permissively.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone, so a directory
	// created before this rule existed would stay world-readable.
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("tighten permissions on %s: %w", dir, err)
	}
	return nil
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

// Save writes the global config, creating the home directory if needed.
func Save(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := EnsureDir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, filePerm)
}
