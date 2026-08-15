// Author: Navjyot Nishant
// Created: 2026-08-15
// Last updated: 2026-08-15
// Description: Notice a newer release once a day, without ever delaying a
// command or making a commit slower.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
)

// checkInterval is how long a result is reused before asking again.
//
// A day, because the thing being detected changes on the order of weeks and
// the cost of asking is an outbound request the user did not ask for. Any
// shorter and the notice becomes the nagging that criterion 7 rules out.
const checkInterval = 24 * time.Hour

// checkTimeout bounds the request.
//
// Two seconds is long enough for a working connection and short enough that
// a captive portal — which accepts the connection and then answers nothing,
// the case a plain dial timeout misses — does not hold up a command that had
// nothing to do with the network. On expiry the check yields nothing and
// says nothing (criterion 11).
const checkTimeout = 2 * time.Second

const latestReleaseURL = "https://api.github.com/repos/navjyotnishant/whodunit/releases/latest"

// reportNewerVersion prints a one-line notice when a newer release exists.
//
// Silent in every other case, and that list is longer than it looks: no
// newer release, the check disabled, no network, a slow or failing request,
// a dev build with no version to compare, or a check that already ran today.
// Criterion 7 — an upgrade notice on every command trains people to skip the
// output, and the output is where everything else this tool says lives.
//
// **Never called from a hook.** The commit path must not make a network
// request: a version check that can hang a commit is worse than an
// out-of-date binary (criterion 11). Hooks run `dun hook ...`, which does
// not reach this.
//
// Failure is silence rather than a warning. The user did not ask about
// releases, so a message saying the check failed is noise about a thing they
// were not doing — and on a machine that deliberately has no outbound
// access, it would be noise on every single command.
func reportNewerVersion(w io.Writer, c *termcolor.Writer, cfg config.Config, current string) {
	if !versionCheckAllowed(cfg) {
		return
	}
	// A dev build has no release to be behind. Comparing "dev" against a
	// tag would either always warn or need a fake version, and both are
	// worse than saying nothing to the person most likely to be building
	// from source.
	if !isReleaseVersion(current) {
		return
	}

	path, err := versionCachePath()
	if err != nil {
		return
	}

	latest, ok := cachedLatest(path)
	if !ok {
		latest, ok = fetchLatest()
		if !ok {
			// Record the attempt even on failure, so a machine with no
			// network tries once a day rather than on every command.
			writeCache(path, "")
			return
		}
		writeCache(path, latest)
	}

	if latest == "" || !newerThan(latest, current) {
		return
	}

	fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, fmt.Sprintf(
		"a newer version is available: %s (you have %s) — run `dun update`",
		latest, current)))
}

// versionCheckAllowed folds the config setting and the environment override
// into one answer.
//
// The environment variable wins, and exists for the machine where editing a
// config file is not the right lever — a locked-down build agent, a shared
// image, a CI job that should make no outbound request whatever the user's
// config says.
func versionCheckAllowed(cfg config.Config) bool {
	if v := os.Getenv("DUN_NO_VERSION_CHECK"); v != "" && v != "0" {
		return false
	}
	// Same reasoning as above for the conventional spelling: a machine-wide
	// opt-out people already set for other tools should be honoured rather
	// than requiring a whodunit-specific one.
	if v := os.Getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return false
	}
	return cfg.VersionCheckEnabled()
}

// isReleaseVersion reports whether a version string is a release tag rather
// than a development build.
//
// scripts/release.sh stamps a v-prefixed tag; a plain `go build` leaves the
// default, which is deliberately not a number that implies a release.
func isReleaseVersion(v string) bool {
	return strings.HasPrefix(v, "v") && len(v) > 1 && v[1] >= '0' && v[1] <= '9'
}

func versionCachePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "version-check"), nil
}

// cachedLatest returns the cached result when it is still fresh.
//
// The file holds a unix timestamp and the tag, so an empty tag records "we
// asked and got nothing" — which is what stops a machine with no network
// from asking again on the next command.
func cachedLatest(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), " ", 2)
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}
	if time.Since(time.Unix(ts, 0)) > checkInterval {
		return "", false
	}
	if len(parts) < 2 {
		return "", true // asked recently, got nothing
	}
	return parts[1], true
}

func writeCache(path, tag string) {
	// Failure here is ignored on purpose: the cache is an optimisation, and
	// a read-only home directory should cost a version check rather than a
	// command.
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d %s", time.Now().Unix(), tag)), 0o600)
}

// fetchLatest asks GitHub for the newest release tag.
//
// Unauthenticated, which is rate-limited per IP — another reason the result
// is cached for a day rather than fetched per command.
func fetchLatest() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	// Bounded read: this is a response from the network, and decoding an
	// unbounded body into memory because a server said so is how a check
	// that should cost nothing becomes the problem.
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", false
	}
	if !isReleaseVersion(body.TagName) {
		return "", false
	}
	return body.TagName, true
}

// newerThan compares two v-prefixed semantic versions.
//
// Numeric per component rather than lexicographic, because a string compare
// puts v0.10.0 before v0.9.0 — and the first time that matters is the
// release where the notice silently stops appearing.
//
// A pre-release suffix (v1.2.0-rc1) compares as its release: someone running
// a release candidate should not be told to upgrade to the version they are
// effectively on, and should be told when the next real one lands.
func newerThan(latest, current string) bool {
	l, c := parseVersion(latest), parseVersion(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return out
		}
		out[i] = n
	}
	return out
}
