// Author: Navjyot Nishant
// Created: 2026-08-12
// Last updated: 2026-08-13
// Description: The guided setup for a shared attribution database, used by
// `dun config datalake` and offered once by `dun init`.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/secret"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newConfigDatalakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "datalake",
		Short: "Set up (or change) where attribution is published.",
		Long: "Walks through connecting whodunit to a shared database — typically a\n" +
			"DevLake instance.\n\n" +
			"What this buys is correlation. whodunit measures what an agent wrote;\n" +
			"it cannot see whether that work shipped, whether it broke, or how long\n" +
			"it took. Those live in GitHub and your issue tracker, and joining them\n" +
			"to attribution is what a shared database is for. Six dashboards ship\n" +
			"with this repository ready to import.\n\n" +
			"It is also the only second copy of what has been recorded.\n\n" +
			"This is optional. Without it everything still works locally: the\n" +
			"journal records normally, `dun report` renders, `dun status` reports.\n" +
			"What needs a target is anything comparing attribution to delivery.\n\n" +
			"The password is encrypted on this machine — never in the config\n" +
			"file, and never exported from your shell profile. It is bound to\n" +
			"this host, so a copied home directory or a restored backup cannot\n" +
			"decrypt it. In CI, set WHODUNIT_SYNC_PASSWORD instead; the\n" +
			"environment always takes precedence.",
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
		"The password is encrypted on this machine, not written to the config"))
	fmt.Fprintln(w, c.S(termcolor.Muted,
		"file and not exported from your shell profile."))
	password, err := askSecret(w, r, "password")
	if err != nil {
		return err
	}

	sync := &config.SyncConfig{
		DSN: fmt.Sprintf("mysql://%s@%s/%s", url.QueryEscape(username), host, database),
		// Named but normally unset. The everyday password is the encrypted
		// one; this exists so CI can inject its own, which it must be able
		// to do — a runner has no encrypted store to read from.
		PasswordEnv: "WHODUNIT_SYNC_PASSWORD",
		OnPush:      true,
	}

	// Stored before the connection test, because the test resolves the DSN
	// through the same path a real sync uses. Testing against a password
	// held only in a local variable would exercise a path that does not
	// exist in production and pass while the stored one was unreadable.
	if password != "" {
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		if err := secret.Store(dir, password); err != nil {
			return fmt.Errorf("store the sync password: %w", err)
		}
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
	if password != "" {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"the password is encrypted on this machine and unreadable elsewhere."))
	} else {
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Warn,
			"note: no password stored — set "+sync.PasswordEnv+" or re-run this to store one."))
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
		"Publish attribution so it can be compared against what shipped?", false) {
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
	fmt.Fprintf(w, "  %-17s %s\n", "delivery impact",
		c.S(termcolor.Muted, "needs a shared database — whether assisted work"))
	fmt.Fprintf(w, "  %-17s %s\n", "",
		c.S(termcolor.Muted, "ships faster or breaks more is a join against"))
	fmt.Fprintf(w, "  %-17s %s\n", "",
		c.S(termcolor.Muted, "GitHub and your issue tracker, not something git knows"))
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

// askSecret reads a password without echoing it.
//
// A password typed in the clear survives in terminal scrollback, in a
// screen recording, and over the shoulder of whoever is standing there,
// which undoes much of the point of encrypting it a moment later.
//
// Echo is suppressed with stty rather than golang.org/x/term to avoid a new
// dependency for one prompt; the package already shells out to ioreg and
// reg for the same reason. On a non-terminal input — a test, a script — the
// read is plain, since there is no echo to suppress.
func askSecret(w io.Writer, r *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(w, "  %s: ", label)

	if isTerminal(os.Stdin) {
		restore, err := suppressEcho()
		if err == nil {
			defer restore()
		}
		// A failure to suppress echo is not fatal: refusing to configure
		// sync because a terminal is unusual would be worse than one
		// visible password. It is said out loud rather than hidden.
		if err != nil {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "  (input will be visible: %v)\n  %s: ", err, label)
		}
	}

	line, err := r.ReadString('\n')
	if isTerminal(os.Stdin) {
		fmt.Fprintln(w)
	}
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// suppressEcho turns terminal echo off and returns a function restoring
// the previous state.
func suppressEcho() (func(), error) {
	// Linux stty spells the flag -F; macOS and the BSDs use -f. Which one
	// works is discovered by trying, rather than branched on GOOS, so a
	// system that disagrees with its own family still works.
	for _, flag := range []string{"-f", "-F"} {
		saved, err := exec.Command("stty", flag, "/dev/tty", "-g").Output()
		if err != nil {
			continue
		}
		if err := sttyRun(flag, "-echo"); err != nil {
			return nil, err
		}
		return sttyRestore(flag, string(saved)), nil
	}
	return nil, fmt.Errorf("stty unavailable")
}

func sttyRun(flag, arg string) error {
	cmd := exec.Command("stty", flag, "/dev/tty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func sttyRestore(flag, saved string) func() {
	return func() { _ = sttyRun(flag, strings.TrimSpace(saved)) }
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
