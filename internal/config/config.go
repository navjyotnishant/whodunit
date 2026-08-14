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
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/navjyotnishant/whodunit/internal/secret"
)

// Config is the global settings file at ~/.whodunit/config.json.
type Config struct {
	// MonthlySpend is the AI agent subscription cost per month, in the
	// smallest currency unit the user reports in (e.g. dollars). Used to
	// compute cost-per-attributed-line; zero means "not configured".
	MonthlySpend float64 `json:"monthly_spend,omitempty"`

	// RetentionDays is how long agent line hashes are kept locally after
	// they have been published.
	//
	// Line hashes only — entries, sessions and metadata are never pruned.
	// That table is roughly 72% of the journal and grows about seven rows
	// per entry, so it is the whole of the growth problem, while the others
	// are what the report's history and after-the-fact verification are
	// built from.
	//
	// Pruning happens after a successful sync and never before one, so
	// anything removed already exists in a second place. A repository that
	// never syncs is never pruned automatically.
	RetentionDays int `json:"retention_days,omitempty"`

	// BackupDays is how many daily copies of the journal are kept.
	//
	// A copy is taken on push, at most once a day, and the oldest is
	// dropped once this many exist. Compressed, seven of them cost a
	// fraction of the live database.
	//
	// Zero means the default rather than "no backups" — a user who wants
	// none says so explicitly with `dun config set backup_days 0`, which
	// is a different thing from having never configured it.
	BackupDays int `json:"backup_days,omitempty"`

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
	//
	// This is now the CI path rather than the everyday one. A developer's
	// password lives encrypted in the whodunit home (internal/secret); the
	// environment variable exists because a CI runner has no such store and
	// injects credentials as variables by design. It still takes
	// precedence, so a runner can override without touching config.
	PasswordEnv string `json:"password_env,omitempty"`

	// OnPush syncs automatically from the pre-push hook.
	OnPush bool `json:"on_push,omitempty"`
}

// Configured reports whether a sync target exists.
func (s *SyncConfig) Configured() bool {
	return s != nil && s.DSN != ""
}

// Resolve returns the DSN with the sync password filled in.
//
// The password comes from the environment first and the encrypted store
// second. That order is deliberate: CI injects credentials as environment
// variables by design, and a runner has no encrypted store to read, so the
// environment has to be able to override. On a developer machine the
// variable is normally unset and the encrypted file answers.
//
// Returns the DSN unchanged when neither source holds anything and no
// variable was named, since a target may legitimately need no password — a
// local database, or a DSN that already carries its own credentials.
func (s *SyncConfig) Resolve() (string, error) {
	if !s.Configured() {
		return "", fmt.Errorf("no sync target configured")
	}

	if s.PasswordEnv != "" {
		if password := os.Getenv(s.PasswordEnv); password != "" {
			return injectPassword(s.DSN, password)
		}
	}

	dir, err := Dir()
	if err != nil {
		return "", err
	}
	password, err := secret.Load(dir)
	switch {
	case err == nil:
		return injectPassword(s.DSN, password)
	case errors.Is(err, secret.ErrNotStored):
		// Nothing stored. A named variable that is also unset is a
		// misconfiguration worth stating plainly rather than leaving as a
		// connection failure to puzzle over later.
		if s.PasswordEnv != "" {
			return "", fmt.Errorf("no sync password found: %s is not set, and none is stored "+
				"(run `dun config datalake` to store one)", s.PasswordEnv)
		}
		return s.DSN, nil
	default:
		return "", err
	}
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

// MinRetentionDays is the shortest retention that does not undermine
// attribution.
//
// The commit hook looks back 30 days for line hashes, so pruning inside that
// window deletes evidence a commit was about to match — silently downgrading
// an intersected commit to observed. Kept as a plain number rather than
// importing attribution, which would make config depend on the package that
// depends on it.
const MinRetentionDays = 30

// defaultRetentionDays is twice the hook's lookback, so a prune can never
// remove a hash still inside the window the hook queries.
const defaultRetentionDays = 60

// defaultBackupDays is a week: long enough that a mistake noticed on Monday
// about last Tuesday is still recoverable.
const defaultBackupDays = 7

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
	if cfg.BackupDays == 0 {
		cfg.BackupDays = defaultBackupDays
	}
	// Below the minimum, not just zero.
	//
	// Validating only in `dun config set` left the file as a way around it:
	// a config written before the minimum existed — or edited by hand —
	// would prune inside the window the commit hook still queries, deleting
	// evidence a commit was about to match. Clamping on read means the
	// guarantee holds however the value got there.
	if cfg.RetentionDays < MinRetentionDays {
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
