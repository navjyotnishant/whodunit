package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"time"

	"github.com/navjyotnishant/whodunit/internal/config"
	"github.com/navjyotnishant/whodunit/internal/hooklog"
	"github.com/navjyotnishant/whodunit/internal/journal"
	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
	"github.com/navjyotnishant/whodunit/internal/sidecar"
	"github.com/navjyotnishant/whodunit/internal/spec"
	"github.com/navjyotnishant/whodunit/internal/termcolor"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show trailer coverage and method mix.",
		Long: "Reports coverage and method mix for a repository's recent commits.\n\n" +
			"Inside a repository, reports that one. Outside any repository, lists\n" +
			"every repository you have instrumented, so a machine-wide view needs\n" +
			"no visiting each one in turn.\n\n" +
			"--repo reports a specific repository from anywhere; it takes a path\n" +
			"or a repo id, the same as `dun journal --repo`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, repoFlag)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path or repo id to report (default: current directory)")
	return cmd
}

func runStatus(cmd *cobra.Command, repoFlag string) error {
	w := cmd.OutOrStdout()

	if repoFlag != "" {
		// Resolve for the error messages, which name what is wrong with a
		// path rather than letting git fail obscurely later.
		_, label, err := resolveRepo(repoFlag)
		if err != nil {
			return err
		}
		dir := repoFlag
		if !isRepoID(repoFlag) {
			return statusFor(w, dir, label)
		}
		// A repo id has no working tree to run git in; the registry knows
		// where it was last seen.
		path, ok := pathForRepoID(repoFlag)
		if !ok {
			return fmt.Errorf("repo id %s is not in the registry, so there is no "+
				"working tree to read git history from", repoFlag[:min(12, len(repoFlag))])
		}
		return statusFor(w, path, path)
	}

	// Inside a repository: report it, as before.
	if inGitRepo("") {
		return statusFor(w, "", "")
	}

	// Outside any repository, listing every instrumented one beats failing
	// with git's own error, which says nothing about what to do.
	return statusAcrossRepos(w)
}

// statusFor reports one repository. An empty dir means the current one.
func statusFor(w io.Writer, dir, label string) error {
	s, err := scanRepo(dir)
	if err != nil {
		return err
	}

	// Repair stale or missing hooks before reporting, so the numbers below
	// are not quietly wrong for the next commit too (NAV-76).
	//
	// `dun status` is where someone looks when they wonder why coverage is
	// lower than expected, and a missing pre-push hook is one real answer
	// to that. Fixing it here means the question and the fix happen in the
	// same breath, rather than the answer being "run another command".
	repairHooks(w, termcolor.New(w), dir)

	if label != "" {
		fmt.Fprintf(w, "%s\n", label)
	}
	fmt.Fprintf(w, "commits examined:  %d\n", s.Total)
	if s.Total == 0 {
		return nil
	}

	c := termcolor.New(w)
	fmt.Fprintf(w, "coverage:          %d/%d (%.0f%%)\n", s.Covered, s.Total, s.CoveragePct())
	fmt.Fprintln(w, "method mix:")
	for _, m := range methodDisplayOrder {
		n := s.MethodCount[m]
		if n == 0 {
			continue
		}
		// Pad before styling: the escape sequences are zero-width on
		// screen but not to %-13s, so styling first breaks alignment.
		label := fmt.Sprintf("%-13s", m)
		// Each line explains itself. "intersected 21" means nothing to
		// someone meeting the vocabulary for the first time, and the
		// person reading their own coverage is exactly who needs to know
		// what it claims.
		fmt.Fprintf(w, "  %s %4d   %s\n",
			c.S(termcolor.MethodStyle(string(m)), label), n,
			c.S(termcolor.Muted, m.Explain()))
	}

	// Said here rather than left to the coverage figure, because the
	// coverage figure cannot say it. Commits older than the first trailer
	// were made before whodunit could observe anything, so they are not
	// evidence that no agent was used - they are evidence of nothing
	// (NAV-21). Someone reading 40% coverage would otherwise reasonably
	// conclude the other 60% was written by hand.
	if s.Unattributed > 0 {
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted, fmt.Sprintf(
			"attribution began %s - %d older commit(s) predate it, "+
				"so AI use before then is unknown, not absent",
			s.FirstAttributed.Format("2006-01-02"), s.Unattributed)))
	}

	// Why the undetermined commits carry no method (WHO-210). The count
	// alone invites the wrong conclusion: someone reading "undetermined
	// 760" reasonably assumes 760 commits nobody used an agent on, when
	// most of them predate the hooks entirely and a handful are a fault
	// worth fixing. The four reasons demand opposite responses, so the
	// summary is useless without them.
	if n := s.MethodCount[spec.MethodUndetermined]; n > 0 {
		// v=2 trailers say why in the status itself. Only the v=1 ones,
		// which recorded nothing, still need the hook-log estimate - so
		// the heading says which of the two a reader is looking at.
		// A v=2 trailer names its own reason; a v=1 one cannot, and the
		// hook log is the only thing that can speak for it. Both kinds
		// coexist in any repository instrumented before the change, so
		// report what each can actually support rather than letting the
		// weaker source decide for both.
		counts := map[spec.Status]int{}
		for _, st := range reasonDisplayOrder {
			counts[st] = s.StatusCount[st]
		}
		heading := "why undetermined:"
		if legacy := s.StatusCount[spec.StatusUndetermined]; legacy > 0 {
			// Estimated only for the ones that carry no answer.
			for st, n := range reasonCounts(dir, s) {
				if counts[st] == 0 {
					counts[st] = n
				}
			}
			heading = "why undetermined (older commits estimated from the hook log):"
		}
		fmt.Fprintln(w, heading)
		for _, r := range reasonDisplayOrder {
			if counts[r] == 0 {
				continue
			}
			fmt.Fprintf(w, "  %s %4d   %s\n",
				c.S(termcolor.Muted, fmt.Sprintf("%-13s", r)), counts[r],
				c.S(termcolor.Muted, r.Explain()))
		}
		// The remainder the hook log cannot account for. Reported rather
		// than folded into a neighbouring reason: a commit whose reason
		// is unknown is not evidence for any particular one, and quietly
		// rounding it into "uninstrumented" would be the same
		// absence-as-evidence mistake one level down.
		if rest := n - total(counts); rest > 0 && len(counts) > 0 {
			fmt.Fprintf(w, "  %s %4d   %s\n",
				c.S(termcolor.Muted, fmt.Sprintf("%-13s", "unclassified")), rest,
				c.S(termcolor.Muted, "the hook log does not reach these commits"))
		}
	}

	printSyncStatus(w, dir)
	return nil
}

// printSyncStatus reports what a sync would publish, and where.
//
// Deliberately not "unsynced since last time". Sync sends the whole journal
// and the sidecar upserts, precisely so there is no local watermark to drift
// out of agreement with the target after a restore. Inventing one here to
// report a delta would reintroduce the state that design avoids.
//
// So the honest question is what the next push would send, which is also the
// one worth answering: it tells you whether the shared dashboards are about
// to gain anything.
func printSyncStatus(w io.Writer, dir string) {
	c := termcolor.New(w)

	cfg, err := config.Load()
	if err != nil {
		return
	}

	if !cfg.Sync.Configured() {
		warnLocalOnly(w, cfg)
		return
	}

	fmt.Fprintln(w, "sync:")

	when := "on push"
	if !cfg.Sync.OnPush {
		when = "manually, with dun sync"
	}

	fmt.Fprintf(w, "  %-13s %s\n",
		c.S(termcolor.Muted, "target"), cfg.Sync.Redacted())
	fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "publishes"), when)

	// The target's own timestamp, from the single-row lookup that also
	// answers "last synced" — one query, and no counting of remote rows.
	synced := lastSyncedAll(cfg.Sync)
	repoID, err := currentRepoID()
	if err != nil {
		return
	}
	last, published := synced[repoID]

	// The backlog: what has been recorded since the target last heard from
	// this repository. Counted in the journal, bounded by that timestamp.
	//
	// This is not the same number as what a sync transmits, and conflating
	// the two is what made this line wrong before: it reported the whole
	// history as pending, so a repository that had just published still
	// claimed thousands of events outstanding.
	dataDir, derr := journalDataDir()
	backlog, backlogSessions := 0, 0
	if derr == nil {
		backlog, backlogSessions, derr = journal.CountSince(dataDir, repoID, last)
	}

	switch {
	case synced == nil:
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "not synced"),
			c.S(termcolor.Muted, "unknown — the target could not be reached"))
	case derr != nil:
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "not synced"),
			c.S(termcolor.Muted, "unknown — the journal could not be read"))
	case !published:
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "not synced"),
			c.S(termcolor.Warn, fmt.Sprintf("%d event(s), %d session(s) — "+
				"this repository has never published", backlog, backlogSessions)))
	case backlog > 0:
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "not synced"),
			c.S(termcolor.Warn, fmt.Sprintf("%d event(s), %d session(s) — recorded since %s",
				backlog, backlogSessions, humanAge(last))))
	default:
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "not synced"),
			c.S(termcolor.Good, "nothing — the target is current"))
	}

	if published {
		fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "last synced"),
			fmt.Sprintf("%s (%s)", humanAge(last), last.Format("2006-01-02 15:04")))
	}

	// What a sync actually transmits, which is the whole journal rather than
	// the backlog: the write is an upsert, so it republishes everything and
	// relies on the target to collapse the duplicates.
	//
	// Stated separately rather than folded into the line above, because the
	// two answer different questions — "how far behind am I" and "what will
	// this command push over the network" — and showing only the second is
	// what made `dun status` read as though nothing had ever synced.
	payload, err := buildPayload(defaultSyncLimit)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "  %-13s %s\n", c.S(termcolor.Muted, "sync sends"),
		c.S(termcolor.Muted, fmt.Sprintf("%d commit(s), %d event(s), %d session(s) "+
			"— the full history each time",
			len(payload.Commits), len(payload.Events), len(payload.Sessions))))
}

// lastSyncedAll asks the target when each repository last published.
//
// The answer lives in the store rather than in a local file, because the
// remote is what actually knows: a local timestamp would record that a sync
// was attempted, not that the rows arrived, and those diverge precisely when
// it matters — a half-failed write, or a database restored from an older
// backup.
//
// One connection and one query for every repository, so the cost does not
// scale with how many are instrumented. It reads whodunit_repos, which holds
// a row per repository, so the cost does not scale with how much history the
// store holds either — a shared database with a team's commits in it answers
// this as fast as an empty one.
//
// Bounded and never fatal. `dun status` runs constantly and must not hang
// because a database is down; nil reads as unknown, which is honest.
func lastSyncedAll(sync *config.SyncConfig) map[string]time.Time {
	dsn, err := sync.Resolve()
	if err != nil {
		return nil
	}

	done := make(chan map[string]time.Time, 1)
	go func() {
		db, err := sidecar.Open(dsn)
		if err != nil {
			done <- nil
			return
		}
		defer db.Close()
		m, err := sidecar.LastSyncAll(db)
		if err != nil {
			done <- nil
			return
		}
		done <- m
	}()

	select {
	case m := <-done:
		return m
	case <-time.After(2 * time.Second):
		// The goroutine finishes on its own and its result is dropped. A
		// status command that stalls on a slow network is worse than one
		// that says it could not tell.
		return nil
	}
}

// truncate shortens s to n characters, marking that it did.
//
// Counting runes rather than bytes: a multi-byte character truncated
// mid-sequence renders as a replacement glyph, which looks like corruption
// rather than elision.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// syncCounts reports what one repository has recorded since it last
// published — the backlog, not its whole history.
//
// The distinction is the whole point of the column. Counting everything ever
// recorded meant a fully-published repository reported thousands of events
// "to sync", which is not merely imprecise: it says publishing did nothing.
//
// `since` is the target's own timestamp for this repository, already fetched
// once for the whole table. Zero means it has never published, and then the
// backlog genuinely is everything.
//
// buildPayload answers this properly for the current repository, but it
// resolves the repo id from the working directory and runs a full git
// analysis — too expensive to repeat for every row of a cross-repo table.
//
// Zero on any failure: this is a column in a report, not a gate.
func syncCounts(dir string, since time.Time) (events, sessions int, ok bool) {
	dataDir, err := journalDataDir()
	if err != nil {
		return 0, 0, false
	}
	repoID, err := repoid.ForRepo(dir)
	if err != nil {
		return 0, 0, false
	}
	events, sessions, err = journal.CountSince(dataDir, repoID, since)
	if err != nil {
		return 0, 0, false
	}
	return events, sessions, true
}

// statusAcrossRepos summarises every instrumented repository, one line each.
func statusAcrossRepos(w io.Writer) error {
	entries, err := registry.List()
	if err != nil {
		return err
	}

	c := termcolor.New(w)
	if len(entries) == 0 {
		// An empty list with no explanation reads like a bug rather than a
		// state, so it says what to do next.
		fmt.Fprintln(w, "no repositories are instrumented yet.")
		fmt.Fprintf(w, "\ninstrument one with:  %s\n", c.S(termcolor.Bold, "dun init"))
		return nil
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	cfg, cfgErr := config.Load()
	syncOn := cfgErr == nil && cfg.Sync.Configured()

	fmt.Fprintf(w, "%d instrumented repositor%s\n\n", len(entries), plural2(len(entries)))

	// Column headers, so a number in the third column is not a guess. The
	// widths below are shared with the rows; changing one means changing
	// both, which is why they sit next to each other.
	fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
		fmt.Sprintf("%-30s %9s  %-26s %-22s %s",
			"repository", "coverage", "method mix", "to sync", "last synced")))

	// Repositories whose hooks are stale or incomplete, collected while
	// walking the list (NAV-76, criterion 3).
	//
	// This listing is the only view that reaches a repository nobody has
	// visited in months, which is exactly where a hook added after
	// instrumentation goes unnoticed — the repository keeps working, just
	// with less attribution than it should have, and nothing says so.
	//
	// Reported rather than repaired: self-repair happens where a person is
	// looking at one repository, and silently rewriting hooks in a dozen
	// repositories from a listing command is more than someone asked for.
	var needUpdate []string

	// Asked once for every repository, before the loop rather than inside it.
	// Nil when sync is off or the target is unreachable, which reads as
	// "unknown" per row without each row paying to find that out again.
	var syncedAll map[string]time.Time
	if syncOn {
		syncedAll = lastSyncedAll(cfg.Sync)
	}

	var available int
	// Methods actually seen, gathered while listing: the legend below
	// explains only these, and rescanning every repository to find them
	// again would double the git calls this command makes.
	seen := map[spec.Method]bool{}

	for _, e := range entries {
		name := shortRepoName(e.Path)

		// A repository can move or be deleted after `dun init` recorded it.
		// Its journal rows survive, so the row is shown as unavailable
		// rather than dropped — the recorded history is still real, and
		// silently omitting it would look like it was never instrumented.
		if !inGitRepo(e.Path) {
			fmt.Fprintf(w, "  %-30s %s\n", name,
				c.S(termcolor.Muted, "moved or deleted — "+e.Path))
			continue
		}
		available++

		s, err := scanRepo(e.Path)
		if err != nil {
			fmt.Fprintf(w, "  %-30s %s\n", name, c.S(termcolor.Warn, "unreadable"))
			continue
		}

		// The sync column answers "would pushing this repository publish
		// anything", which is the question a cross-repo view is for. It is
		// journal rows, not commits: a repository can have plenty of
		// recorded agent activity and no commits carrying it yet.
		pending, synced := "", ""
		if syncOn {
			// The target's timestamp first: it decides both columns, and
			// the backlog is only meaningful relative to it.
			var last time.Time
			published := false
			if id, err := repoid.ForRepo(e.Path); err == nil && syncedAll != nil {
				last, published = syncedAll[id]
			}

			switch {
			case syncedAll == nil:
				synced = "—"
			case published:
				synced = humanAge(last)
			default:
				synced = "never"
			}

			// A zero `last` counts everything, which is right for a
			// repository that has never published.
			events, sessions, ok := syncCounts(e.Path, last)
			switch {
			case !ok:
				pending = "—"
			case events == 0:
				pending = "nothing"
			default:
				pending = fmt.Sprintf("%d ev, %d sess", events, sessions)
			}
		}

		if gitDir, err := gitDirFor(e.Path); err == nil {
			if missing, stale := staleHooks(gitDir); len(missing) > 0 || len(stale) > 0 {
				needUpdate = append(needUpdate, name)
			}
		}

		if s.Total == 0 {
			fmt.Fprintf(w, "  %-30s %9s  %-26s %-22s %s\n", name,
				c.S(termcolor.Muted, "—"), c.S(termcolor.Muted, "no commits yet"),
				c.S(termcolor.Muted, pending), c.S(termcolor.Muted, synced))
			continue
		}

		for m, n := range s.MethodCount {
			if n > 0 {
				seen[m] = true
			}
		}
		// Padded before styling: escape sequences are zero-width on screen
		// but not to %-34s, so styling first breaks every column after it.
		// Truncated too — one long mix would push every later column out of
		// line for every row, and the full breakdown is one --repo away.
		mix := fmt.Sprintf("%-26s", truncate(methodSummary(s), 26))
		fmt.Fprintf(w, "  %-30s %8.0f%%  %s %-22s %s\n",
			name, s.CoveragePct(), c.S(termcolor.Muted, mix),
			pending, c.S(termcolor.Muted, synced))
	}

	if len(needUpdate) > 0 {
		fmt.Fprintf(w, "\n  %s\n", c.S(termcolor.Warn, fmt.Sprintf(
			"%d repositor%s missing a hook or running an older one: %s",
			len(needUpdate), plural2(len(needUpdate)), strings.Join(needUpdate, ", "))))
		fmt.Fprintf(w, "  %s\n", c.S(termcolor.Muted,
			"they are attributing less than they could — fix all of them with:  dun repos update"))
	}

	// Where the numbers in the last column would go, stated once rather
	// than repeated on every row.
	if syncOn {
		fmt.Fprintf(w, "\n  %s %s\n", c.S(termcolor.Muted, "syncing to"), cfg.Sync.Redacted())
	} else if cfgErr == nil {
		warnLocalOnly(w, cfg)
	}

	if available > 0 {
		// The per-method gloss the single-repository view prints does not
		// fit on a line shared with several repositories, so the meanings
		// are given once underneath rather than dropped entirely — the
		// names mean nothing to someone meeting them here first.
		printMethodLegend(w, seen)
		// The names above are shortened to fit the column, so they cannot
		// be pasted into --repo. Saying where the real ones are beats
		// leaving someone to guess at the path.
		fmt.Fprintf(w, "\n%s\n", c.S(termcolor.Muted,
			"one repository in detail:  dun status --repo <path>"))
		fmt.Fprintf(w, "%s\n", c.S(termcolor.Muted,
			"full paths to paste:       dun repos list"))
	}
	return nil
}

// printMethodLegend explains the methods that actually appear above.
// Listing all five would explain levels this machine has never produced.
func printMethodLegend(w io.Writer, seen map[spec.Method]bool) {
	if len(seen) == 0 {
		return
	}
	c := termcolor.New(w)

	fmt.Fprintln(w)
	for _, m := range methodDisplayOrder {
		if !seen[m] {
			continue
		}
		label := fmt.Sprintf("%-13s", m)
		fmt.Fprintf(w, "  %s %s\n",
			c.S(termcolor.MethodStyle(string(m)), label),
			c.S(termcolor.Muted, m.Explain()))
	}
}

// coverageStats is one repository's trailer coverage.
type coverageStats struct {
	Total       int
	Covered     int
	MethodCount map[spec.Method]int

	// StatusCount is what a v=2 trailer says about itself. Method grades
	// evidence; status says whether there is any and why not, and since
	// WHO-211 the reason is recorded rather than estimated afterwards.
	StatusCount map[spec.Status]int

	// FirstAttributed is the date of the oldest commit in the scanned
	// window that carries a trailer, and Unattributed counts the commits
	// older than it.
	//
	// Both exist to answer a question the coverage figure cannot: a
	// repository instrumented last week has months of commits that
	// predate attribution, and those are not evidence that no agent was
	// used. They are evidence of nothing, which is a different claim
	// (NAV-21). Reporting the span makes the gap visible rather than
	// leaving someone to read 40% coverage as 60% human-written.
	FirstAttributed time.Time
	Unattributed    int
}

func (s coverageStats) CoveragePct() float64 {
	if s.Total == 0 {
		return 0
	}
	return 100 * float64(s.Covered) / float64(s.Total)
}

var methodDisplayOrder = []spec.Method{
	spec.MethodIntersected, spec.MethodObserved, spec.MethodInferred,
	spec.MethodDeclared, spec.MethodUndetermined,
}

// reasonDisplayOrder runs finding, then usually-fine, then gap, then
// fault - so the line a reader most needs to act on is last and closest
// to whatever follows. Not a confidence ladder: these are four unrelated
// answers, and ordering them by severity is the only ordering that means
// anything.
var reasonDisplayOrder = []spec.Status{
	spec.StatusUnassisted, spec.StatusUnmatched,
	spec.StatusUninstrumented, spec.StatusDegraded,
}

// methodSummary renders the mix compactly, strongest evidence first, for a
// one-line-per-repository listing.
func methodSummary(s coverageStats) string {
	var parts []string
	for _, m := range methodDisplayOrder {
		if n := s.MethodCount[m]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", m, n))
		}
	}
	if len(parts) == 0 {
		return "no trailers"
	}
	return strings.Join(parts, ", ")
}

// scanRepo reads trailer coverage from a repository's recent commits. An
// empty dir means the current working directory.
func scanRepo(dir string) (coverageStats, error) {
	s := coverageStats{MethodCount: map[spec.Method]int{}, StatusCount: map[spec.Status]int{}}

	// Author date alongside the message: the boundary between "before
	// attribution existed" and "after" is a date, and without it the
	// unattributed span cannot be distinguished from genuine non-use.
	cmd := exec.Command("git", "log", "-n", "100", "--format=%aI%x01%B%x00")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// An unborn repository is a valid zero-commit status, not a
		// failure — anything else genuinely is.
		if exitErr, ok := err.(*exec.ExitError); ok &&
			strings.Contains(string(exitErr.Stderr), "does not have any commits") {
			return s, nil
		}
		return s, fmt.Errorf("read git log: %w", err)
	}

	prefix := spec.TrailerKey + ":"
	for _, record := range strings.Split(string(out), "\x00") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		when, commitMsg, ok := strings.Cut(record, "\x01")
		if !ok {
			continue
		}
		at, _ := time.Parse(time.RFC3339, strings.TrimSpace(when))
		s.Total++

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
			s.Covered++
			s.MethodCount[t.Method]++
			s.StatusCount[t.Status]++
			// git log is newest-first, so the last trailer seen is the
			// oldest one, which is where attribution began.
			if !at.IsZero() {
				s.FirstAttributed = at
			}
			break
		}
	}

	// Counted after the scan rather than during it: the boundary is only
	// known once the oldest trailer has been seen.
	if !s.FirstAttributed.IsZero() {
		for _, record := range strings.Split(string(out), "\x00") {
			when, _, ok := strings.Cut(strings.TrimSpace(record), "\x01")
			if !ok {
				continue
			}
			if at, err := time.Parse(time.RFC3339, strings.TrimSpace(when)); err == nil &&
				at.Before(s.FirstAttributed) {
				s.Unattributed++
			}
		}
	}
	return s, nil
}

// inGitRepo reports whether dir is inside a git working tree. An empty dir
// means the current directory.
func inGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// isRepoID reports whether s looks like a repo id rather than a path.
func isRepoID(s string) bool { return looksLikeRepoID(s) }

// pathForRepoID finds where a registered repository lives.
func pathForRepoID(repoID string) (string, bool) {
	entries, err := registry.List()
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.RepoID == repoID {
			return e.Path, true
		}
	}
	return "", false
}

// shortRepoName is the last two path segments, which identifies a
// repository without a column of identical parent directories.
func shortRepoName(path string) string {
	// Split on either separator rather than the platform's own.
	//
	// os.PathSeparator is '\' on Windows, so a path written with forward
	// slashes — which Go's APIs accept, and which arrives from config files,
	// git, and anything MSYS-flavoured — did not split at all and the whole
	// path was printed where two segments belong.
	normalized := strings.ReplaceAll(path, `\`, "/")
	parts := strings.Split(strings.TrimSuffix(normalized, "/"), "/")
	if len(parts) <= 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// reasonCounts classifies this repository's unattributed commits by why
// they carry no method, reading the hook log the hooks already write.
//
// RECONSTRUCTED, AND APPROXIMATE. The log records what each determination
// concluded but not which commit it belonged to, so there is no SHA to
// join on and these are counts of determinations, not of commits. They
// differ: an amend re-runs the hook, so one commit can produce several
// entries, and a determination made before the window under examination
// is excluded by time rather than by identity.
//
// So the proportions are reliable and the totals are not. That is worth
// having - "these are mostly pre-install, and two are a fault" is the
// question people actually ask - but it is not a measurement, and the
// caller labels it as an estimate rather than implying otherwise.
// WHO-211 records the reason at determination time, where the commit is
// known and no reconstruction is needed.
//
// A repository with no readable log returns nothing, and the caller
// reports every commit as unclassified — which is honest, and visibly
// different from claiming they were all human-written.
func reasonCounts(dir string, s coverageStats) map[spec.Status]int {
	out := map[spec.Status]int{}

	// Everything before the first trailer. Needs no log: whodunit was not
	// installed, so its silence says nothing either way (NAV-21).
	if s.Unattributed > 0 {
		out[spec.StatusUninstrumented] = s.Unattributed
	}

	home, err := config.Dir()
	if err != nil {
		return out
	}
	entries, err := hooklog.Read(home, 0)
	if err != nil {
		return out
	}

	repoID, err := repoid.ForRepo(dir)
	if err != nil {
		return out
	}
	// Bounded to the window the caller examined. The log spans every
	// determination ever made in this repository, while the coverage
	// figures cover the last N commits, so counting the whole log against
	// them reported more reasons than there were commits.
	for _, e := range entries {
		if e.RepoID != repoID || e.Event != "determine" {
			continue
		}
		if !s.FirstAttributed.IsZero() && e.Time.Before(s.FirstAttributed) {
			continue
		}
		if e.Level == hooklog.LevelWarn {
			out[spec.StatusDegraded]++
			continue
		}
		if !strings.HasPrefix(e.Detail, "undetermined") {
			continue
		}
		switch {
		case strings.Contains(e.Detail, "no agent activity"):
			out[spec.StatusUnassisted]++
		case agentLinesPresent(e.Detail):
			out[spec.StatusUnmatched]++
		}
	}

	return out
}

// agentLinesPresent reports whether a determination had agent lines to
// match against. That is the whole distinction between "an agent was
// working elsewhere" and "no agent was anywhere near this": both end in
// undetermined, and only this number separates them.
func agentLinesPresent(detail string) bool {
	i := strings.Index(detail, " agent line(s)")
	if i < 0 {
		return false
	}
	j := strings.LastIndexByte(detail[:i], ' ')
	if j < 0 {
		return false
	}
	n, err := strconv.Atoi(detail[j+1 : i])
	return err == nil && n > 0
}

func total(counts map[spec.Status]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}
