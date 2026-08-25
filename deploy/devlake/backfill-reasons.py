#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-24
# Last updated: 2026-08-24
# Description: Classify why each unattributed commit carries no attribution.

"""Split `undetermined` into the four states it actually conflates.

WHO-210. Measured on this database on 2026-08-24: 760 of 985 commits are
`undetermined`, and that single value covers four unrelated situations —

  uninstrumented  the hooks were not installed when the commit was made
  unmatched       an agent was active, but touched none of the staged files
  unassisted      the hooks ran, the journal was readable, and no agent
                  had been anywhere near this work
  degraded        attribution itself failed

Only the last two are worth acting on, and today a reader cannot tell any
of them apart.

`unassisted` is the one that changes what the tool can say. Every
competitor infers adoption from code style, seat telemetry or trailers, so
none of them can separate "no agent was used" from "we did not see one".
This project can — but only where it was demonstrably watching, which is
what makes the guard below the important part of this script rather than a
formality.

RECONSTRUCTED, NOT MEASURED. Everything here is derived after the fact
from the hook log and from each repository's first attributed commit. A
value written at determination time would be evidence; this is an
inference about evidence. WHO-211 does it properly, at the point where the
answer is actually known.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone
from collections import Counter

HOOKLOG = os.path.expanduser("~/.whodunit/log/hooks.log")

# "undetermined via undetermined, 2 staged file(s), 25272 agent line(s)"
AGENT_LINES = re.compile(r"(\d+) agent line\(s\)")


def epoch(ts):
    """Seconds since the epoch, from either side's timestamp format.

    The hook log writes an offset-aware local time; MySQL is asked for UTC
    with no offset at all. Both are normalised here so the join happens on
    an instant rather than on how that instant was spelled.
    """
    if ts.endswith("Z"):
        ts = ts[:-1] + "+00:00"
    dt = datetime.fromisoformat(ts)
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return int(dt.timestamp())


def hooklog_entries(path):
    """Every determination the hooks recorded, oldest first.

    A malformed line is skipped rather than fatal: this log is written by
    a hook that must never fail a commit, so a truncated final line is an
    expected state rather than corruption.
    """
    if not os.path.exists(path):
        return []
    out = []
    with open(path) as fh:
        for line in fh:
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            if e.get("event") == "determine":
                out.append(e)
    return out


def classify(detail):
    """The reason a single determination produced no attribution.

    Reads only what the hook already recorded. The agent-line count is
    what separates the two interesting cases: lines present means an
    agent was active and its work simply is not in these files, while no
    lines at all means nothing was there to match.
    """
    if not detail.startswith("undetermined"):
        return None
    if "no agent activity" in detail:
        return "unassisted"
    m = AGENT_LINES.search(detail)
    if m and int(m.group(1)) > 0:
        return "unmatched"
    # A determination that named no agent-line count at all. Not
    # classified rather than guessed - the log did not say.
    return None


def mysql(sql, container, db="lake"):
    r = subprocess.run(
        ["docker", "exec", container, "mysql", "-umerico", "-pmerico", db, "-N", "-e", sql],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        sys.exit(f"mysql failed: {r.stderr.strip()}")
    return [line.split("\t") for line in r.stdout.strip().splitlines() if line]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--container", default="devlake-mysql-1")
    ap.add_argument("--apply", action="store_true",
                    help="write the reasons; without it, only report what would change")
    args = ap.parse_args()

    entries = hooklog_entries(HOOKLOG)
    if not entries:
        sys.exit(f"no hook log at {HOOKLOG} - nothing to reconstruct from")

    # The window the log actually covers. Outside it, a commit's reason
    # can only come from the instrumentation boundary, and `unassisted`
    # is never assertable however much it might look like one.
    covered_from = min(e["time"] for e in entries)
    print(f"hook log covers {covered_from} onward, {len(entries)} determination(s)")

    # Reasons the log settles, as (repo_id, epoch-seconds) -> reason.
    #
    # Both sides are parsed to epoch rather than compared as text. The log
    # writes a local offset (-05:00) and MySQL renders UTC, so the same
    # instant appears five hours apart as a string. Slicing the two and
    # comparing them matched nothing at all, silently - which is the
    # failure this whole ticket is about, arriving in its own backfill.
    by_repo_time = {}
    counts = Counter()
    for e in entries:
        reason = classify(e.get("detail", ""))
        if e.get("level") == "warn":
            # A warn on any hook stage is attribution failing rather than
            # finding nothing. None exist in this database; the branch is
            # here so the state is representable rather than theoretical.
            reason = "degraded"
        if not reason:
            continue
        by_repo_time[(e.get("repo_id", ""), epoch(e["time"]))] = reason
        counts[reason] += 1

    print("\nfrom the hook log:")
    for k, v in sorted(counts.items()):
        print(f"  {v:5}  {k}")

    # Everything before a repository's first attributed commit. This needs
    # no log: whodunit was not installed, so the absence of attribution
    # says nothing about whether an agent was used (NAV-21).
    rows = mysql("""
        SELECT c.repo_id, c.commit_sha,
               DATE_FORMAT(FROM_UNIXTIME(c.committed_at/1000000000), '%Y-%m-%dT%H:%i:%s')
        FROM whodunit_commits c
        WHERE c.method = 'undetermined'
          AND c.committed_at < (SELECT MIN(c2.committed_at) FROM whodunit_commits c2
                                 WHERE c2.repo_id = c.repo_id AND c2.method <> 'undetermined')
    """, args.container)
    print(f"  {len(rows):5}  uninstrumented (before each repo's first attributed commit)")

    updates = [(sha, repo, "uninstrumented") for repo, sha, _ in rows]

    # Commits after instrumentation began, matched to the log by time.
    after = mysql("""
        SELECT c.repo_id, c.commit_sha,
               DATE_FORMAT(FROM_UNIXTIME(c.committed_at/1000000000), '%Y-%m-%dT%H:%i:%s')
        FROM whodunit_commits c
        WHERE c.method = 'undetermined'
          AND c.committed_at >= (SELECT MIN(c2.committed_at) FROM whodunit_commits c2
                                  WHERE c2.repo_id = c.repo_id AND c2.method <> 'undetermined')
    """, args.container)

    matched = unmatched_rows = 0
    for repo, sha, ts in after:
        # git stamps committed_at first and the hook logs afterwards, so
        # the log entry lands a few seconds AFTER the commit time -
        # measured across this database: median +3s, p90 +5s. Search a
        # window either side and take the nearest, because a long-running
        # commit can stretch the gap and an adjacent commit must not be
        # allowed to claim this one's entry.
        reason = None
        commit_at = epoch(ts)
        best = None
        for delta in range(-2, 31):
            hit = by_repo_time.get((repo, commit_at + delta))
            if hit and (best is None or abs(delta) < abs(best[0])):
                best = (delta, hit)
        if best:
            reason = best[1]
        if reason:
            updates.append((sha, repo, reason))
            matched += 1
        else:
            unmatched_rows += 1

    print(f"\n  {matched:5}  classified from the log")
    print(f"  {unmatched_rows:5}  left NULL - the log does not settle these")

    # THE GUARD. `unassisted` asserts a human wrote this, which is the one
    # claim here that can be wrong in the direction that flatters us. It
    # is only defensible where the log proves the tooling was watching.
    for sha, repo, reason in updates:
        if reason == "unassisted":
            hit = any(k[0] == repo for k in by_repo_time)
            if not hit:
                sys.exit(f"refusing to write unassisted for {sha[:8]}: "
                         "no hook-log coverage proves the tooling was watching")

    if not args.apply:
        print("\ndry run - pass --apply to write")
        return

    for sha, repo, reason in updates:
        mysql(f"UPDATE whodunit_commits SET reason='{reason}' "
              f"WHERE commit_sha='{sha}' AND repo_id='{repo}'", args.container)
    print(f"\nwrote {len(updates)} reason(s)")

    final = mysql("""
        SELECT COALESCE(reason,'(null)'), COUNT(*) FROM whodunit_commits
        WHERE method='undetermined' GROUP BY reason ORDER BY COUNT(*) DESC
    """, args.container)
    print("\nundetermined commits by reason:")
    for reason, n in final:
        print(f"  {int(n):5}  {reason}")




def selftest():
    """The guard that matters, exercised rather than asserted.

    `unassisted` is the only reason here that makes a positive claim - it
    says a human wrote this code. Asserting it where the tooling was not
    watching would be the exact NAV-21 error, in the direction that
    flatters the tool, so the refusal is checked rather than trusted.
    """
    assert classify("undetermined via undetermined, 2 staged file(s), 25272 agent line(s)") \
        == "unmatched", "agent lines present means the agent worked elsewhere"
    assert classify("undetermined: no agent activity found in the last 7 days") \
        == "unassisted", "no agent activity at all means a human wrote it"
    assert classify("assisted via intersected, 6 staged file(s), 25004 agent line(s)") \
        is None, "an attributed commit has no reason to classify"
    assert classify("undetermined via undetermined, 2 staged file(s)") \
        is None, "no agent-line count means the log did not say - never guess"

    # A repo the log has never seen cannot yield `unassisted`, however
    # much its commits look unattributed.
    seen = {("known-repo", 1000): "unassisted"}
    assert not any(k[0] == "unknown-repo" for k in seen), \
        "a repo outside hook-log coverage must not be classifiable as unassisted"
    print("selftest ok")


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        selftest()
    else:
        main()
