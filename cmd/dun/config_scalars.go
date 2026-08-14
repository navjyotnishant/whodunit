// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The settings `dun config set` understands beyond agent paths.

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
)

// scalarSetting is one configurable value that is not an agent path.
//
// A table rather than a switch, so `set`, `get` and the "unknown setting"
// message all read the same list. They drifted before: the report told people
// to run `dun config set monthly_spend`, which the command rejected, because
// the advice and the accepted keys were written in different places.
type scalarSetting struct {
	Key  string
	Help string

	// Get renders the current value, and Set parses and applies one.
	// Returning an error from Set rather than validating elsewhere keeps
	// the message next to the parsing that produced it.
	Get func(config.Config) string
	Set func(*config.Config, string) error
}

func scalarSettings() []scalarSetting {
	return []scalarSetting{
		{
			Key:  "monthly_spend",
			Help: "what the agent subscriptions cost per month, for cost-per-line",
			Get:  func(c config.Config) string { return strconv.FormatFloat(c.MonthlySpend, 'f', -1, 64) },
			Set: func(c *config.Config, v string) error {
				f, err := strconv.ParseFloat(strings.TrimPrefix(v, "$"), 64)
				if err != nil {
					return fmt.Errorf("monthly_spend must be a number, got %q", v)
				}
				if f < 0 {
					return fmt.Errorf("monthly_spend cannot be negative")
				}
				c.MonthlySpend = f
				return nil
			},
		},
		{
			Key:  "backup_days",
			Help: "how many daily copies of the journal to keep (0 disables)",
			Get:  func(c config.Config) string { return strconv.Itoa(c.BackupDays) },
			Set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("backup_days must be a whole number, got %q", v)
				}
				if n < 0 {
					return fmt.Errorf("backup_days cannot be negative")
				}
				c.BackupDays = n
				return nil
			},
		},
		{
			Key: "retention_days",
			Help: "how long agent line hashes are kept locally after publishing " +
				"(minimum 30, the hook's lookback)",
			Get: func(c config.Config) string { return strconv.Itoa(c.RetentionDays) },
			Set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("retention_days must be a whole number of days, got %q", v)
				}
				// Below the hook's own lookback, pruning would delete
				// hashes a commit was about to match — silently turning an
				// intersected commit into an observed one. Refusing beats
				// accepting a value that quietly degrades attribution.
				if n < config.MinRetentionDays {
					return fmt.Errorf("retention_days must be at least %d: the commit hook "+
						"looks back %d days, and pruning inside that window would "+
						"downgrade attribution it was about to record",
						config.MinRetentionDays, config.MinRetentionDays)
				}
				c.RetentionDays = n
				return nil
			},
		},
	}
}

func scalarByKey(key string) (scalarSetting, bool) {
	for _, s := range scalarSettings() {
		if s.Key == key {
			return s, true
		}
	}
	return scalarSetting{}, false
}

// settingKeys lists everything `dun config set` accepts, for the error
// message someone sees after guessing wrong.
func settingKeys() string {
	keys := []string{"agent.<name>.path"}
	for _, s := range scalarSettings() {
		keys = append(keys, s.Key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
