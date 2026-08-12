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
	"net/url"
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

	// Agents overrides where an agent's transcripts are looked for, keyed
	// by the agent name that appears in the trailer ("claude-code",
	// "codex"). Empty means "use the built-in default for that agent".
	//
	// This exists because a wrong path fails silently: no error, no
	// sessions, every commit undetermined — which reads as "no AI was
	// used" rather than "the collector could not look" (NAV-21). An
	// override turns that from a dead end into a one-line fix.
	Agents map[string]AgentConfig `json:"agents,omitempty"`

	// Sync is where attribution is published, and whether pushing does it
	// automatically. Absent means local-only: the journal, the reports and
	// the CLI all work, and nothing leaves the machine.
	Sync *SyncConfig `json:"sync,omitempty"`
}

// SyncConfig points at a shared database — typically a DevLake instance.
//
// A pointer on Config rather than a value, so "never configured" and
// "configured and then emptied" stay distinguishable in the file.
type SyncConfig struct {
	// DSN identifies the target: driver, host, port, user, database. It
	// deliberately carries no password.
	DSN string `json:"dsn,omitempty"`

	// PasswordEnv names the environment variable holding the password.
	//
	// The password itself is never written here. config.json is already
	// owner-only, but a credential in it still ends up in backups, in
	// synced dotfiles, and pasted into issue reports — none of which the
	// file permissions cover.
	PasswordEnv string `json:"password_env,omitempty"`

	// OnPush syncs automatically from the pre-push hook.
	OnPush bool `json:"on_push,omitempty"`
}

// Configured reports whether a sync target exists.
func (s *SyncConfig) Configured() bool {
	return s != nil && s.DSN != ""
}

// Resolve returns the DSN with the password from PasswordEnv filled in.
//
// Returns the DSN unchanged when no password variable is named, since a
// target may legitimately need no password — a local database, or a DSN
// that already carries its own credentials.
//
// Reports an error when a variable is named but unset: that is a
// misconfiguration worth stating rather than a connection failure to
// puzzle over later.
func (s *SyncConfig) Resolve() (string, error) {
	if !s.Configured() {
		return "", fmt.Errorf("no sync target configured")
	}
	if s.PasswordEnv == "" {
		return s.DSN, nil
	}
	password := os.Getenv(s.PasswordEnv)
	if password == "" {
		return "", fmt.Errorf("%s is not set (the sync password comes from that environment variable)", s.PasswordEnv)
	}
	return injectPassword(s.DSN, password)
}

// injectPassword puts the password into a DSN's userinfo, leaving every
// other part untouched.
func injectPassword(dsn, password string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("sync dsn is not a valid url: %w", err)
	}
	user := ""
	if u.User != nil {
		user = u.User.Username()
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
}

// Redacted returns the DSN with any password replaced, for printing.
// Nothing should ever print a resolved DSN: it goes into terminal
// scrollback, CI logs, and screenshots.
func (s *SyncConfig) Redacted() string {
	if !s.Configured() {
		return ""
	}
	u, err := url.Parse(s.DSN)
	if err != nil {
		return s.DSN
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "****")
		}
	}
	return u.String()
}

// AgentConfig is per-agent settings. One field today; a struct rather than
// a bare string so adding another (enabled, poll interval) does not change
// the file format for everyone.
type AgentConfig struct {
	// Path is the directory this agent stores transcripts under, replacing
	// the built-in default.
	Path string `json:"path,omitempty"`
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
