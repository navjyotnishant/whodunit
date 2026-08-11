package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/navjyotnishant/whodunit/internal/daemon"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "daemon",
		Short: "Foreground watcher that keeps the journal in sync with agent activity.",
	}
	root.AddCommand(newDaemonRunCmd())
	return root
}

func newDaemonRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Watch this repo's Claude Code sessions and ingest continuously. Ctrl-C to stop.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			out := cmd.OutOrStdout()
			log := func(msg string) { fmt.Fprintln(out, msg) }

			log(fmt.Sprintf("watching %s (ctrl-c to stop)", cwd))
			return daemon.Run(ctx, cwd, func() (int, error) {
				return runIngestCount()
			}, log)
		},
	}
}
