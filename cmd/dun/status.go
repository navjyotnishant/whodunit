package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show trailer coverage and method mix for recent commits.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd)
		},
	}
}

func runStatus(cmd *cobra.Command) error {
	out, err := exec.Command("git", "log", "-n", "100", "--format=%B%x00").Output()
	if err != nil {
		// An empty/unborn repo (no commits yet) is a valid zero-commit
		// status, not a failure — anything else genuinely is.
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			fmt.Fprintln(cmd.OutOrStdout(), "commits examined:  0")
			return nil
		}
		return fmt.Errorf("read git log: %w", err)
	}

	total := 0
	covered := 0
	methodCount := map[spec.Method]int{}
	prefix := spec.TrailerKey + ":"

	for _, commitMsg := range strings.Split(string(out), "\x00") {
		commitMsg = strings.TrimSpace(commitMsg)
		if commitMsg == "" {
			continue
		}
		total++

		scanner := bufio.NewScanner(strings.NewReader(commitMsg))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			t, err := spec.Parse(strings.TrimSpace(line[len(prefix):]))
			if err != nil {
				continue
			}
			covered++
			methodCount[t.Method]++
			break
		}
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "commits examined:  %d\n", total)
	if total == 0 {
		return nil
	}
	fmt.Fprintf(w, "coverage:          %d/%d (%.0f%%)\n", covered, total, 100*float64(covered)/float64(total))
	fmt.Fprintln(w, "method mix:")
	for _, m := range []spec.Method{spec.MethodIntersected, spec.MethodObserved, spec.MethodInferred, spec.MethodDeclared, spec.MethodUndetermined} {
		if n := methodCount[m]; n > 0 {
			fmt.Fprintf(w, "  %-13s %d\n", m, n)
		}
	}
	return nil
}
