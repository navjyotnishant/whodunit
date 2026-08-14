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
			"Run `dun config` with no arguments to see every setting, its value,\n" +
			"and whether that value was chosen or defaulted.\n\n" +
			"Two settings have their own commands because they are more than a\n" +
			"value: `dun config datalake` walks through a sync target, and\n" +
			"`dun config agents` reports where each agent's transcripts were\n" +
			"found. A sync password is never set here — datalake prompts for it\n" +
			"without echo and stores it encrypted, bound to this machine.",
		Example: "  # see everything, and where each value came from\n" +
			"  dun config\n\n" +
			"  # what the agents cost per month, for cost-per-line\n" +
			"  dun config set monthly_spend 350\n\n" +
			"  # daily journal copies to keep; 0 turns backups off\n" +
			"  dun config set backup_days 14\n\n" +
			"  # how long line hashes stay local after publishing.\n" +
			"  # refuses anything under 30 — the commit hook looks back that\n" +
			"  # far, and pruning inside the window would delete evidence a\n" +
			"  # commit was about to match\n" +
			"  dun config set retention_days 90\n\n" +
			"  # where an agent keeps its transcripts, when it is not the\n" +
			"  # usual place. a wrong path produces no attribution at all,\n" +
			"  # with nothing to indicate why\n" +
			"  dun config set agent.claude-code.path /path/to/projects\n\n" +
			"  # read one value\n" +
			"  dun config get retention_days",
		// Bare `dun config` shows the settings rather than prose about
		// them. Printing help there meant the one command named after the
		// configuration could not tell you what your configuration was —
		// the answer lived in a file you had to know to open.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown config command %q — run `dun config --help`", args[0])
			}
			return runConfigList(cmd.OutOrStdout())
		},
		SilenceUsage: true,
	}
	root.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigAgentsCmd(), newConfigDatalakeCmd())
	return root
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a setting.",
		// The keys are listed by settingKeys() rather than written here, so
		// this cannot fall behind what set actually accepts — which it did:
		// the report advised `dun config set monthly_spend` while the
		// command rejected the key.
		Long: "Set one setting. Known settings:\n\n  " + settingKeysList() +
			"\n\nRun `dun config` to see the current values.",
		Example: "  dun config set monthly_spend 350\n" +
			"  dun config set backup_days 14\n" +
			"  dun config set retention_days 90\n" +
			"  dun config set agent.claude-code.path /path/to/projects",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args[0], args[1])
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Example: "  dun config get retention_days\n" +
			"  dun config get agent.claude-code.path",
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
	// Scalars first: they are the settings people go looking for, and until
	// now the only way to set one was to edit the JSON by hand.
	if sc, ok := scalarByKey(key); ok {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := sc.Set(&cfg, value); err != nil {
			return err
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", key, sc.Get(cfg))
		return nil
	}

	agent := agentPathKey(key)
	if agent == "" {
		return fmt.Errorf("unknown setting %q — known settings: %s", key, settingKeys())
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
	if sc, ok := scalarByKey(key); ok {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), sc.Get(cfg))
		return nil
	}

	agent := agentPathKey(key)
	if agent == "" {
		return fmt.Errorf("unknown setting %q — known settings: %s", key, settingKeys())
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
