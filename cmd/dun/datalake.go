// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-12
// Description: The guided setup for a shared attribution database, used by
// `dun config datalake` and offered once by `dun init`.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newConfigDatalakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "datalake",
		Short: "Set up (or change) where attribution is published.",
		Long: "Walks through connecting whodunit to a shared database — typically a\n" +
			"DevLake instance — so team dashboards can read what your commits\n" +
			"recorded.\n\n" +
			"This is optional. Without it everything still works locally: the\n" +
			"journal records normally, `dun report` renders, `dun status` reports.\n" +
			"Only the shared dashboards need a target.\n\n" +
			"The password is never written to disk. You name an environment\n" +
			"variable and whodunit reads it when it syncs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatalakeSetup(cmd.OutOrStdout(), cmd.InOrStdin())
		},
	}
}

// runDatalakeSetup asks for a target, tests it, and saves it.
func runDatalakeSetup(w io.Writer, in io.Reader) error {
	c := termcolor.New(w)
	r := bufio.NewReader(in)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.Sync.Configured() {
		fmt.Fprintf(w, "currently publishing to %s\n\n", c.S(termcolor.Bold, cfg.Sync.Redacted()))
	}

	fmt.Fprintln(w, "Where should attribution be published?")
	fmt.Fprintln(w, c.S(termcolor.Muted, "  a DevLake instance, or any MySQL database you control"))
	fmt.Fprintln(w)

	host := ask(w, r, "host and port", "localhost:3306")
	database := ask(w, r, "database", "lake")
	username := ask(w, r, "username", "merico")

	fmt.Fprintln(w)
	fmt.Fprintln(w, c.S(termcolor.Muted,
		"The password is not stored in the config file. Name an environment"))
	fmt.Fprintln(w, c.S(termcolor.Muted,
		"variable holding it, and whodunit will read that when it syncs."))
	passwordEnv := ask(w, r, "password variable", "WHODUNIT_SYNC_PASSWORD")

	sync := &config.SyncConfig{
		DSN:         fmt.Sprintf("mysql://%s@%s/%s", url.QueryEscape(username), host, database),
		PasswordEnv: passwordEnv,
		OnPush:      true,
	}

	// Test before saving, so a typo surfaces here rather than as a warning
	// on some later push that nobody reads.
	fmt.Fprintln(w)
	fmt.Fprint(w, "testing the connection... ")
	if err := testSync(sync); err != nil {
		fmt.Fprintln(w, c.S(termcolor.Warn, "failed"))
		fmt.Fprintf(w, "  %v\n\n", err)

		if !confirm(w, r, "save it anyway?", false) {
			fmt.Fprintln(w, "not saved.")
			return nil
		}
	} else {
		fmt.Fprintln(w, c.S(termcolor.Good, "ok"))
	}

	cfg.Sync = sync
	if err := config.Save(cfg); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s attribution will be sent when you push.\n",
		c.S(termcolor.Good, "sync configured."))
	if passwordEnv != "" && os.Getenv(passwordEnv) == "" {
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Warn,
			"note: "+passwordEnv+" is not set in this shell yet."))
	}
	return nil
}

// testSync opens the target and checks the schema can be created, which is
// what a real sync does first. A reachable database whose user cannot
// create tables would otherwise pass a connection check and fail on push.
func testSync(s *config.SyncConfig) error {
	dsn, err := s.Resolve()
	if err != nil {
		return err
	}
	db, err := sidecar.Open(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	return sidecar.EnsureSchema(db)
}

// offerDatalakeSetup is what `dun init` calls: report an existing target,
// or offer to configure one.
//
// Returns without asking anything when stdin is not a terminal. `dun init`
// runs in CI and in scripts, and a wizard that blocks on stdin there is a
// hung build rather than a prompt.
func offerDatalakeSetup(w io.Writer, in io.Reader) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	c := termcolor.New(w)

	if cfg.Sync.Configured() {
		// Say it every time, not only at setup. A global target means this
		// repository starts publishing the moment it is instrumented, with
		// no separate opt-in — that should be visible when it becomes true.
		fmt.Fprintln(w)
		fmt.Fprintf(w, "publishing to %s %s\n",
			c.S(termcolor.Bold, cfg.Sync.Redacted()),
			c.S(termcolor.Muted, "(on push)"))
		return
	}

	if !isTerminal(in) {
		return
	}

	fmt.Fprintln(w)
	if !confirm(w, bufio.NewReader(in),
		"Send attribution to a shared database so team dashboards work?", false) {
		printLocalOnlyNotice(w)
		return
	}
	if err := runDatalakeSetup(w, in); err != nil {
		// Setup failing must not fail an install that already succeeded.
		fmt.Fprintf(w, "setup did not complete: %v\n", err)
	}
}

// printLocalOnlyNotice says what declining costs, and what it does not.
//
// "No dashboards" alone reads as "the tool does not work without a
// server", which is false and the opposite of this project's pitch. Naming
// what still works matters as much as naming what does not.
func printLocalOnlyNotice(w io.Writer) {
	c := termcolor.New(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "skipped. attribution stays on this machine.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-17s %s\n", "dun report", c.S(termcolor.Muted, "works — local HTML report"))
	fmt.Fprintf(w, "  %-17s %s\n", "dun status", c.S(termcolor.Muted, "works — coverage in the terminal"))
	fmt.Fprintf(w, "  %-17s %s\n", "team dashboards", c.S(termcolor.Muted, "need a shared database"))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "add one later:  %s\n", c.S(termcolor.Bold, "dun config datalake"))
}

// ask prompts for one value, returning def when the answer is empty.
func ask(w io.Writer, r *bufio.Reader, label, def string) string {
	c := termcolor.New(w)
	fmt.Fprintf(w, "  %-20s %s ", label, c.S(termcolor.Muted, "["+def+"]"))
	line, _ := r.ReadString('\n')
	if v := strings.TrimSpace(line); v != "" {
		return v
	}
	return def
}

// confirm asks a yes/no question.
func confirm(w io.Writer, r *bufio.Reader, question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(w, "%s %s ", question, hint)
	line, _ := r.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// isTerminal reports whether r is an interactive terminal. Anything that
// is not a *os.File — a buffer in a test, a pipe in a script — is not.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
