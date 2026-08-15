package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/spf13/cobra"
)

func newCheckCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Fail if any commit since --base lacks a valid AI-Attribution trailer. For CI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd, base)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base ref to compare against (required)")
	cmd.MarkFlagRequired("base")
	return cmd
}

func runCheck(cmd *cobra.Command, base string) error {
	// --no-merges, because a merge commit cannot carry an attribution
	// trailer and failing it is a gate that can never pass.
	//
	// A merge introduces no changes of its own — its content is the two
	// parents, each already checked on the branch it came from — so there
	// is nothing for the hook to attribute even in principle. Worse, the
	// merges that matter here are made server-side by GitHub's "Merge pull
	// request" button, where no local hook runs at all.
	//
	// This failed on its own repository: three merge commits, one of them
	// the release that had just shipped, reported as "missing
	// AI-Attribution trailer" on every subsequent pull request. A required
	// check that fails permanently is worse than no check, because the
	// fix people reach for is to stop requiring it.
	out, err := exec.Command("git", "log", "--no-merges", base+"..HEAD", "--format=%H%x01%B%x00").Output()
	if err != nil {
		return fmt.Errorf("read git log: %w", err)
	}

	var missing, invalid []string
	prefix := spec.TrailerKey + ":"

	for _, record := range strings.Split(string(out), "\x00") {
		record = strings.TrimRight(record, "\n")
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x01", 2)
		if len(parts) != 2 {
			continue
		}
		sha, msg := parts[0][:min(8, len(parts[0]))], parts[1]

		var trailerLine string
		scanner := bufio.NewScanner(strings.NewReader(msg))
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, prefix) {
				trailerLine = strings.TrimSpace(line[len(prefix):])
			}
		}

		switch {
		case trailerLine == "":
			missing = append(missing, sha)
		default:
			if _, err := spec.Parse(trailerLine); err != nil {
				invalid = append(invalid, fmt.Sprintf("%s (%v)", sha, err))
			}
		}
	}

	w := cmd.OutOrStdout()
	if len(missing) == 0 && len(invalid) == 0 {
		fmt.Fprintln(w, "all commits carry a valid AI-Attribution trailer")
		return nil
	}
	for _, sha := range missing {
		fmt.Fprintf(w, "%s: missing %s trailer\n", sha, spec.TrailerKey)
	}
	for _, entry := range invalid {
		fmt.Fprintf(w, "%s: invalid trailer\n", entry)
	}
	return fmt.Errorf("%d commit(s) failed the trailer check", len(missing)+len(invalid))
}
