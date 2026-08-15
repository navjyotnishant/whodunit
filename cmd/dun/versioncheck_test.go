package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// off returns a Config with the version check explicitly disabled.
func off() config.Config {
	no := false
	return config.Config{VersionCheck: &no}
}

func TestVersionComparisonIsNumericNotLexicographic(t *testing.T) {
	// The case a string compare gets wrong, and the reason this is not
	// `latest > current`: "v0.10.0" sorts before "v0.9.0" as text, so the
	// notice would silently stop appearing at the first double-digit minor.
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.10.0", "v0.9.0", true},
		{"v0.9.0", "v0.10.0", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.1", "v0.2.0", true},
		{"v0.2.0", "v0.2.1", false},
		{"v1.2.3", "v1.2.3", false},

		// A pre-release compares as its release: someone on v1.2.0-rc1
		// should not be told to upgrade to v1.2.0, which is what they are
		// effectively running.
		{"v1.2.0", "v1.2.0-rc1", false},
		{"v1.2.1", "v1.2.0-rc1", true},
	} {
		if got := newerThan(tc.latest, tc.current); got != tc.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v",
				tc.latest, tc.current, got, tc.want)
		}
	}
}

// Criterion 12: disabled means it never runs — no request, no output.
//
// Asserted through the public entry point rather than by calling
// versionCheckAllowed directly, because the thing that matters is that
// nothing reaches the network, and a test of the predicate alone would
// still pass if the caller ignored it.
func TestDisabledCheckSaysNothing(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	var buf bytes.Buffer
	reportNewerVersion(&buf, termcolor.New(&buf), off(), "v0.1.0")

	if buf.Len() != 0 {
		t.Errorf("said %q with the check disabled; criterion 12 says it must "+
			"never run", buf.String())
	}
}

func TestEnvironmentOverrideDisablesTheCheck(t *testing.T) {
	// Both spellings: the whodunit-specific one, and the cross-tool
	// convention someone may already have set machine-wide.
	for _, env := range []string{"DUN_NO_VERSION_CHECK", "DO_NOT_TRACK"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "1")
			if versionCheckAllowed(config.Config{}) {
				t.Errorf("%s=1 did not disable the check", env)
			}
		})
	}

	// "0" is not "set to a truthy value" — someone exporting DO_NOT_TRACK=0
	// is saying the opposite of opting out, and treating any non-empty
	// value as a disable would invert their intent.
	t.Run("zero does not disable", func(t *testing.T) {
		t.Setenv("DO_NOT_TRACK", "0")
		if !versionCheckAllowed(config.Config{}) {
			t.Error("DO_NOT_TRACK=0 disabled the check; 0 means do not opt out")
		}
	})
}

// Criterion 11, the half that would otherwise be discovered in production:
// a dev build must not be told it is out of date.
//
// Without this, every developer running a locally-built binary sees an
// upgrade notice on every bare `dun` — and the version they are told to
// upgrade to is one they may be ahead of.
func TestDevBuildIsNeverToldToUpgrade(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	var buf bytes.Buffer
	// "dev" is version.go's default for a plain `go build`.
	reportNewerVersion(&buf, termcolor.New(&buf), config.Config{}, "dev")

	if buf.Len() != 0 {
		t.Errorf("told a dev build to upgrade: %q", buf.String())
	}
}

func TestOnlyReleaseTagsCount(t *testing.T) {
	for _, v := range []string{"dev", "", "v", "unknown", "0.2.0", "vNext"} {
		if isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = true; only a v-prefixed number is a release", v)
		}
	}
	for _, v := range []string{"v0.1.0", "v1.2.3", "v0.2.0-rc1"} {
		if !isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = false, want true", v)
		}
	}
}

// The cache is what keeps this from being a request per command, so its
// freshness window is asserted rather than assumed.
func TestCacheIsReusedWithinTheDayAndExpiresAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "version-check")

	write := func(age time.Duration, tag string) {
		stamp := time.Now().Add(-age).Unix()
		if err := os.WriteFile(path,
			[]byte(fmt.Sprintf("%d %s", stamp, tag)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(time.Hour, "v9.9.9")
	got, ok := cachedLatest(path)
	if !ok || got != "v9.9.9" {
		t.Errorf("fresh cache: got (%q, %v), want (v9.9.9, true)", got, ok)
	}

	write(48*time.Hour, "v9.9.9")
	if _, ok := cachedLatest(path); ok {
		t.Error("a two-day-old cache was reused; it should expire after a day")
	}

	// A failed check writes an empty tag so a machine with no network asks
	// once a day rather than on every command. That must read back as
	// "asked recently, nothing to report" — not as a cache miss, which
	// would defeat the whole point.
	write(time.Hour, "")
	tag, ok := cachedLatest(path)
	if !ok {
		t.Error("an empty cached result was treated as a miss, so a machine " +
			"with no network would re-request on every command")
	}
	if tag != "" {
		t.Errorf("got tag %q from an empty cache entry", tag)
	}
}

// A missing or corrupt cache file must be a miss, not a crash: this runs on
// the path of an ordinary command.
func TestUnreadableCacheIsJustAMiss(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"", "garbage", "notanumber v1.0.0"} {
		path := filepath.Join(dir, "c")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := cachedLatest(path); ok {
			t.Errorf("cache content %q was accepted", content)
		}
	}
	if _, ok := cachedLatest(filepath.Join(dir, "does-not-exist")); ok {
		t.Error("a missing cache file was treated as a hit")
	}
}

// Criterion 11: the check must not delay a command.
//
// Bounded well under the wall-clock budget of a CLI invocation. This does
// make a real request when the network is up, which is the point — the
// timeout is the thing being tested, and mocking the transport would assert
// the mock rather than the constant.
func TestTheCheckCannotHangACommand(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		var buf bytes.Buffer
		reportNewerVersion(&buf, termcolor.New(&buf), config.Config{}, "v0.0.1")
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("took %v; the check must not delay a command", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the version check hung — criterion 11 says it must never " +
			"delay a command, and this would hang every bare `dun`")
	}
}

// Criterion 12 again, from the config side: the setting has to round-trip,
// including the explicit-off case that a plain bool would lose.
func TestVersionCheckSettingRoundTrips(t *testing.T) {
	s, ok := scalarByKey("version_check")
	if !ok {
		t.Fatal("version_check is not a known setting, so it cannot be turned off")
	}

	var cfg config.Config
	if got := s.Get(cfg); got != "on" {
		t.Errorf("unset reads as %q, want on — the check defaults to enabled", got)
	}

	if err := s.Set(&cfg, "off"); err != nil {
		t.Fatal(err)
	}
	if cfg.VersionCheckEnabled() {
		t.Error("set off, still enabled")
	}
	if got := s.Get(cfg); got != "off" {
		t.Errorf("after off, reads as %q", got)
	}

	// An explicit "on" must be recorded rather than left nil. Both read as
	// enabled, but only one survives as a deliberate choice in the file.
	if err := s.Set(&cfg, "on"); err != nil {
		t.Fatal(err)
	}
	if cfg.VersionCheck == nil {
		t.Error("an explicit `on` was not written, so it reads back as " +
			"never-configured rather than as a decision")
	}

	if err := s.Set(&cfg, "maybe"); err == nil {
		t.Error("accepted a value that is neither on nor off")
	}
}

// The notice names the command that acts on it. A message telling someone
// they are out of date without saying what to run is a dead end.
func TestTheNoticeNamesTheFix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHODUNIT_HOME", dir)

	// Seed a fresh cache so no request is made. Asserting on a seeded tag
	// rather than a live one is the difference between testing the notice
	// and testing today's release.
	if err := os.WriteFile(filepath.Join(dir, "version-check"),
		[]byte(fmt.Sprintf("%d v99.0.0", time.Now().Unix())), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	reportNewerVersion(&buf, termcolor.New(&buf), config.Config{}, "v0.1.0")

	out := buf.String()
	if out == "" {
		t.Fatal("said nothing with a newer version cached")
	}
	for _, want := range []string{"v99.0.0", "v0.1.0", "dun update"} {
		if !strings.Contains(out, want) {
			t.Errorf("notice %q does not mention %q", out, want)
		}
	}
}
