// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: `dun config` — read and set global settings, including where
// each agent's transcripts live.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/adapter"
	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Read and change global settings.",
		Long: "Settings live in ~/.whodunit/config.json.\n\n" +
			"The setting most worth knowing about is where each agent keeps its\n" +
			"transcripts. whodunit knows the usual location for every agent it\n" +
			"supports, but a machine that stores them elsewhere would otherwise\n" +
			"produce no attribution at all, with nothing to indicate why:\n\n" +
			"  dun config set agent.claude-code.path /path/to/projects\n\n" +
			"Run `dun config agents` to see what was found and where it looked.",
	}
	root.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigAgentsCmd())
	return root
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a setting, e.g. agent.claude-code.path",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args[0], args[1])
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one setting's value.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(cmd, args[0])
		},
	}
}

func newConfigAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "Show which agents were found, and where whodunit looked.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			printDetections(cmd.OutOrStdout(), adapter.Detect(cwd), true)
			return nil
		},
	}
}

// agentPathKey parses "agent.<name>.path" into the agent name.
// Returns "" when the key is not an agent path setting.
func agentPathKey(key string) string {
	rest, ok := strings.CutPrefix(key, "agent.")
	if !ok {
		return ""
	}
	name, ok := strings.CutSuffix(rest, ".path")
	if !ok || name == "" {
		return ""
	}
	return name
}

func runConfigSet(cmd *cobra.Command, key, value string) error {
	agent := agentPathKey(key)
	if agent == "" {
		return fmt.Errorf("unknown setting %q (try agent.<name>.path)", key)
	}
	if adapter.ByName(agent) == nil {
		return fmt.Errorf("unknown agent %q — known agents: %s", agent, strings.Join(agentNames(), ", "))
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]config.AgentConfig{}
	}
	a := cfg.Agents[agent]
	a.Path = value
	cfg.Agents[agent] = a

	if err := config.Save(cfg); err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s = %s\n", key, value)

	// Warn rather than refuse. A path can legitimately point at a drive
	// that is not mounted right now — a real case on Windows and WSL — so
	// this is checked at use time, not gated at set time. Saying nothing
	// would let a typo sit undiscovered until someone wonders why nothing
	// is attributed.
	if info, err := os.Stat(value); err != nil || !info.IsDir() {
		fmt.Fprintf(w, "warning: %s does not exist yet\n", value)
	}
	return nil
}

func runConfigGet(cmd *cobra.Command, key string) error {
	agent := agentPathKey(key)
	if agent == "" {
		return fmt.Errorf("unknown setting %q (try agent.<name>.path)", key)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if a, ok := cfg.Agents[agent]; ok && a.Path != "" {
		fmt.Fprintln(cmd.OutOrStdout(), a.Path)
		return nil
	}
	// Not configured is not an error: report the default in use, so the
	// answer to "where does it look" is always a path.
	if ad := adapter.ByName(agent); ad != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "%s (default)\n", ad.SessionDir(""))
	}
	return nil
}

func agentNames() []string {
	var names []string
	for _, a := range adapter.All() {
		names = append(names, a.Name())
	}
	return names
}
