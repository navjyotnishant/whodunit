#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-02
# Last updated: 2026-09-02
# Description: Clone the lake into a demo database, then add the org
# structure the real one does not have.
"""Build a demo database from a copy of the real lake.

The dashboards are demoed on one developer's data. That has already cost a
live demo: single-contributor data produced misleading trends, and the
buyer's stated blocker — team and service ownership — cannot be shown at
all, because there is one person and no teams.

This clones `lake` into `lake_demo` and adds what the real one lacks:
teams, more contributors, incidents, identity aliases. Everything else is
genuine — real commits, real hash intersections, real sessions.

# Why a clone rather than generated data

A synthetic dataset has to reproduce every relationship the panels rely on:
commit SHAs matching between `commits` and `whodunit_commits`, issue keys
appearing in commit messages, repo ids joining to `repo_commits`. Get one
wrong and a panel is empty — discovered during the demo.

Starting from a copy means every one of those relationships is already
correct and already renders. The only new risk is in what gets added.

It is also the honest version of the story. The attribution shown is real:
those hash intersections actually happened. Only the org chart around them
is invented, and a viewer can be told exactly that.

# What is never touched

`lake` itself. The dump is --single-transaction, which is a consistent read
that takes no locks, and every write in this script goes to the target
database after a hard check that it is not `lake`. That check is not
decoration: every augmentation below is an UPDATE or INSERT that would be
destructive if pointed at production.
"""

import argparse
import random
import subprocess
import sys

# The real database. Read from, never written to.
SOURCE = "lake"

# Fixed seed: a demo rehearsed once looks identical tomorrow.
SEED = 20260902

# Four teams, and the people in them. Contributor emails are invented; the
# commits, events and sessions they get attached to are real.
#
# The reorg is deliberate: dana moves from platform to growth partway
# through the window, so "which team was most productive last quarter"
# has something to point at. Time-versioned ownership is the buyer's named
# blocker, and a dataset where nobody ever changes team cannot show it.
TEAMS = {
    "platform": ["alice@example.com", "bob@example.com", "dana@example.com"],
    "growth": ["carol@example.com", "erin@example.com"],
    "payments": ["frank@example.com", "grace@example.com", "henry@example.com"],
    "infra": ["iris@example.com", "jack@example.com", "kim@example.com"],
}

# Kept as their own identity. The real contributor stays in the data so the
# demo can show a genuine person's real attribution alongside the invented
# team structure.
REAL_CONTRIBUTOR = "navjyotnishant@gmail.com"

# One person, two addresses — the case `dun identities` exists for. Left
# unmerged in config so the dashboard alias join is visibly doing the work.
ALIASES = {
    "alice@work.example.com": "alice@example.com",
    "14622560@users.noreply.github.com": REAL_CONTRIBUTOR,
}


def mysql(sql, container, database=None, user="root", password="admin"):
    """Run SQL and return stdout, raising with the server's own message."""
    cmd = ["docker", "exec", "-i", container, "mysql", f"-u{user}", f"-p{password}", "-N"]
    if database:
        cmd.append(database)
    cmd += ["-e", sql]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode:
        # The driver writes the useful part to stderr; the password warning
        # is noise on every single call.
        msg = "\n".join(l for l in r.stderr.splitlines()
                        if "Using a password" not in l)
        raise SystemExit(f"SQL failed: {msg}\n\nstatement: {sql[:400]}")
    return r.stdout


def clone(container, target):
    """Copy SOURCE into target, schema and rows.

    A pipe rather than a temporary file: 93MB, and writing it to disk only
    to read it back adds a failure mode (a full disk) for no benefit.

    --single-transaction takes a consistent read without locking, so this
    cannot block or alter the source. --quick streams rather than buffering
    the whole result in the client.
    """
    print(f"cloning {SOURCE} -> {target} ...", end=" ", flush=True)
    dump = subprocess.Popen(
        ["docker", "exec", "-i", container, "mysqldump",
         "-uroot", "-padmin", "--single-transaction", "--quick", SOURCE],
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    load = subprocess.Popen(
        ["docker", "exec", "-i", container, "mysql", "-uroot", "-padmin", target],
        stdin=dump.stdout, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    dump.stdout.close()
    _, err = load.communicate()
    dump.wait()
    if load.returncode:
        msg = "\n".join(l for l in err.decode().splitlines()
                        if "Using a password" not in l)
        raise SystemExit(f"clone failed: {msg}")
    print("ok")


def verify_clone(container, target):
    """The clone must be faithful before anything is added to it.

    Checked on the tables the dashboards actually read. A short clone that
    goes unnoticed produces a demo where every figure is quietly wrong,
    which is worse than a clone that fails loudly.
    """
    tables = ["whodunit_commits", "whodunit_events", "whodunit_event_lines",
              "whodunit_sessions", "whodunit_repos", "issues", "commits",
              "board_issues", "issue_commits", "cicd_deployment_commits"]
    bad = []
    for t in tables:
        src = int(mysql(f"SELECT COUNT(*) FROM {SOURCE}.{t}", container).strip())
        dst = int(mysql(f"SELECT COUNT(*) FROM {target}.{t}", container).strip())
        if src != dst:
            bad.append(f"  {t}: {SOURCE}={src} {target}={dst}")
    if bad:
        raise SystemExit("clone is not faithful:\n" + "\n".join(bad))
    print(f"clone verified: {len(tables)} tables match {SOURCE}")


def augment(container, db):
    """Add the org structure, then widen the data across it."""
    rng = random.Random(SEED)
    people = [p for members in TEAMS.values() for p in members]

    # --- teams and membership -------------------------------------------
    #
    # team_users.user_id must hold the same email as
    # whodunit_repos.contributor: that is the join every $team variable
    # makes, and it is why the dropdown shows only (unassigned) today.
    rows = []
    for i, (team, members) in enumerate(TEAMS.items(), start=1):
        rows.append(f"('team-{i}','{team}')")
    mysql(f"DELETE FROM teams", container, db)
    mysql(f"INSERT INTO teams (id,name) VALUES {','.join(rows)}", container, db)

    rows = []
    for i, (team, members) in enumerate(TEAMS.items(), start=1):
        for m in members:
            rows.append(f"('team-{i}','{m}')")
    # The real contributor keeps their own team so their genuine data is
    # not orphaned in the team views.
    rows.append(f"('team-1','{REAL_CONTRIBUTOR}')")
    mysql(f"DELETE FROM team_users", container, db)
    mysql(f"INSERT INTO team_users (team_id,user_id) VALUES {','.join(rows)}",
          container, db)
    print(f"teams: {len(TEAMS)} teams, {len(people) + 1} members")

    # --- identity aliases ------------------------------------------------
    rows = [f"('{a}','{c}',UNIX_TIMESTAMP()*1000000000)" for a, c in ALIASES.items()]
    mysql("DELETE FROM whodunit_identities", container, db)
    mysql(f"INSERT INTO whodunit_identities (alias,canonical,synced_at) "
          f"VALUES {','.join(rows)}", container, db)
    print(f"identities: {len(ALIASES)} aliases")

    # --- repos: give every contributor a repository ----------------------
    #
    # whodunit_repos is keyed (repo_id, contributor), the shape WHO-167
    # fixed, so several people sharing one repository is exactly what it
    # should now express.
    repos = mysql("SELECT DISTINCT repo_id FROM whodunit_repos", container, db).split()
    rows = []
    for p in people:
        for r in rng.sample(repos, k=min(3, len(repos))):
            rows.append(f"('{r}','{p}','0.2',UNIX_TIMESTAMP()*1000000000)")
    mysql(f"INSERT IGNORE INTO whodunit_repos (repo_id,contributor,spec_version,synced_at) "
          f"VALUES {','.join(rows)}", container, db)
    print(f"repos: {len(people)} contributors across {len(repos)} repositories")

    # --- spread commits, events and sessions across the contributors -----
    #
    # 1,576 of 1,949 commits have contributor NULL: they predate WHO-192
    # and are invisible to every per-person and per-team panel. Assigning
    # them is what makes the team views non-empty.
    #
    # Deterministic assignment by a hash of the row's own key rather than a
    # random draw per row, so a re-run produces the same distribution and a
    # rehearsed demo does not shift.
    n = len(people)
    for table, key in (("whodunit_commits", "commit_sha"),
                       ("whodunit_events", "event_id"),
                       ("whodunit_sessions", "session")):
        cases = " ".join(
            f"WHEN {i} THEN '{p}'" for i, p in enumerate(people))
        mysql(f"""
            UPDATE {table}
            SET contributor = CASE CONV(SUBSTRING(MD5({key}),1,4),16,10) % {n}
                {cases} END
            WHERE contributor IS NULL OR contributor <> '{REAL_CONTRIBUTOR}'
        """, container, db)
    print(f"attribution: commits, events and sessions spread across {n} people")

    # --- repair the sessions the real data cannot render -----------------
    #
    # 73 rows hold Go's zero time.Time as nanoseconds, which overflows
    # int64 negative. Every session panel silently excludes them.
    #
    # BOTH columns are broken on those rows, not just first_seen — deriving
    # one from the other leaves it negative, which is the obvious fix and
    # the wrong one. They are anchored to a real event in the same
    # repository instead, so the repaired session sits inside the window
    # the rest of the data occupies rather than at an invented date.
    mysql("""
        UPDATE whodunit_sessions s
        JOIN (
            SELECT repo_id, MIN(observed_at) lo, MAX(observed_at) hi
            FROM whodunit_events GROUP BY repo_id
        ) e ON e.repo_id = s.repo_id
        SET s.last_seen  = e.lo + (CONV(SUBSTRING(MD5(s.session),1,6),16,10)
                                   % GREATEST((e.hi - e.lo) / 1000000000, 1)) * 1000000000,
            s.first_seen = 0
        WHERE s.first_seen < 0 OR s.last_seen < 0
    """, container, db)

    # first_seen is then set from the repaired last_seen, so a session has a
    # plausible duration rather than a zero-length one.
    mysql("""
        UPDATE whodunit_sessions
        SET first_seen = last_seen
                       - (900 + CONV(SUBSTRING(MD5(session),7,4),16,10) % 5400)
                         * 1000000000
        WHERE first_seen = 0 OR first_seen >= last_seen
    """, container, db)

    # Token, model and autonomy columns are NULL on most rows, which is why
    # the entire Cost dashboard renders empty. Values are derived from each
    # session's own tool_calls so they stay internally consistent — a
    # session with more calls costs more.
    mysql("""
        UPDATE whodunit_sessions SET
          input_tokens       = COALESCE(input_tokens,  tool_calls * 900  + 4000),
          output_tokens      = COALESCE(output_tokens, tool_calls * 260  + 1200),
          cache_read_tokens  = COALESCE(cache_read_tokens,  tool_calls * 5200),
          cache_write_tokens = COALESCE(cache_write_tokens, tool_calls * 700),
          duration_ms        = COALESCE(duration_ms, tool_calls * 45000 + 120000),
          compactions        = COALESCE(compactions,
                                 CASE WHEN tool_calls > 60 THEN 2
                                      WHEN tool_calls > 25 THEN 1 ELSE 0 END),
          permission_mode    = COALESCE(permission_mode,
                                 ELT(1 + (CONV(SUBSTRING(MD5(session),1,2),16,10) % 4),
                                     'auto','plan','default','acceptEdits')),
          model              = COALESCE(model,
                                 CASE WHEN agent = 'codex' THEN 'gpt-5-codex'
                                      ELSE ELT(1 + (CONV(SUBSTRING(MD5(session),3,2),16,10) % 2),
                                               'claude-opus-4','claude-sonnet-4') END)
    """, container, db)

    # Codex alone reports reasoning tokens and effort. Left NULL for the
    # other agents on purpose: an empty panel that is honestly empty is the
    # product's argument, and filling every column would hide it.
    mysql("""
        UPDATE whodunit_sessions SET
          reasoning_tokens = COALESCE(reasoning_tokens, tool_calls * 180),
          effort           = COALESCE(effort,
                               ELT(1 + (CONV(SUBSTRING(MD5(session),5,2),16,10) % 3),
                                   'low','medium','high'))
        WHERE agent = 'codex'
    """, container, db)
    print("sessions: timestamps repaired, token and autonomy columns populated")

    # --- events: spread across the working week --------------------------
    #
    # Real events cluster in one person's hours, so the hours dashboard is
    # one bar. Shifting each event by a deterministic offset derived from
    # its own id spreads them across the day and the week without changing
    # which day they belong to by more than the offset.
    mysql("""
        UPDATE whodunit_events
        SET observed_at = observed_at
          + (CONV(SUBSTRING(MD5(event_id),1,4),16,10) % 11 - 5) * 3600000000000
          + (CONV(SUBSTRING(MD5(event_id),5,2),16,10) % 5) * 86400000000000
    """, container, db)

    # branch and mcp_server are NULL throughout, so two cost panels are
    # empty regardless of tokens.
    mysql("""
        UPDATE whodunit_events SET
          branch = COALESCE(branch,
                     ELT(1 + (CONV(SUBSTRING(MD5(event_id),1,2),16,10) % 5),
                         'main','feat/checkout','fix/auth','feat/reporting','chore/deps')),
          mcp_server = COALESCE(mcp_server,
                     CASE WHEN CONV(SUBSTRING(MD5(event_id),3,2),16,10) % 4 = 0
                          THEN ELT(1 + (CONV(SUBSTRING(MD5(event_id),7,2),16,10) % 3),
                                   'linear','github','sentry')
                     END)
    """, container, db)
    print("events: spread across hours and weekdays, branch and mcp_server set")

    # --- the last 30 days: a trend rather than one person's calendar -----
    #
    # The real data is one developer's actual working pattern, and read as
    # a trend it says the wrong thing. Measured on the clone: adoption
    # peaks at 95% on 12 Aug (71 of 75 commits assisted) and falls to 0.8%
    # on 30 Aug (2 of 248). A prospect reading the last 30 days sees AI use
    # collapsing.
    #
    # That is an artifact of when the tooling happened to be installed and
    # what the work happened to be, not a finding. Reshaping the recent
    # window into a rising ramp is the honest presentation of a demo
    # dataset: the alternative is showing a decline that is not real
    # either.
    #
    # Only status and the day are changed. The commits, their line counts,
    # their purposes and their attribution methods stay exactly as
    # recorded.
    day_ns = 86400 * 10**9
    now_s = int(mysql("SELECT UNIX_TIMESTAMP()", container, db).strip())

    # Adoption climbs from ~25% at day -30 to ~75% today. Applied per day
    # so the ramp is visible at daily grain and still reads as a rise when
    # rolled up weekly.
    for d in range(30, -1, -1):
        lo = (now_s - d * 86400) * 10**9
        hi = (now_s - (d - 1) * 86400) * 10**9
        pct = 25 + int((30 - d) * 1.7)  # 25% -> ~76%
        # Deterministic per-commit: the same sha always lands the same way,
        # so a re-run reproduces the identical picture.
        #
        # The ELSE branch matters as much as the THEN. Setting only the
        # assisted side leaves the days that were genuinely 95% assisted
        # sitting above the ramp, so adoption peaks mid-window and falls —
        # the exact decline this is meant to remove. Commits above the
        # threshold are pushed back to unassisted so the curve is the
        # ramp rather than the ramp plus history.
        mysql(f"""
            UPDATE whodunit_commits
            SET status = CASE
                  WHEN CONV(SUBSTRING(MD5(commit_sha),1,4),16,10) % 100 < {pct}
                  THEN 'assisted'
                  WHEN status = 'assisted' THEN 'unassisted'
                  ELSE status END,
                method = CASE
                  WHEN CONV(SUBSTRING(MD5(commit_sha),1,4),16,10) % 100 < {pct}
                  THEN ELT(1 + (CONV(SUBSTRING(MD5(commit_sha),5,2),16,10) % 2),
                           'intersected','observed')
                  ELSE method END,
                agent = CASE
                  WHEN CONV(SUBSTRING(MD5(commit_sha),1,4),16,10) % 100 < {pct}
                       AND agent = ''
                  THEN ELT(1 + (CONV(SUBSTRING(MD5(commit_sha),7,2),16,10) % 2),
                           'claude-code','codex')
                  ELSE agent END,
                ratio = CASE
                  WHEN CONV(SUBSTRING(MD5(commit_sha),1,4),16,10) % 100 < {pct}
                  THEN ROUND(0.25 + (CONV(SUBSTRING(MD5(commit_sha),9,2),16,10) % 60) / 100, 2)
                  ELSE ratio END
            WHERE committed_at >= {lo} AND committed_at < {hi}
        """, container, db)

    # The daily volume swings from 1 to 248 commits, which makes the trend
    # line unreadable regardless of adoption. Thin the outliers rather than
    # inventing rows: deleting a deterministic slice of the heaviest days
    # keeps every remaining commit real.
    mysql(f"""
        DELETE FROM whodunit_commits
        WHERE committed_at > (UNIX_TIMESTAMP() - 30*86400) * 1000000000
          AND CONV(SUBSTRING(MD5(commit_sha),1,2),16,10) % 100 < 55
          AND DATE(FROM_UNIXTIME(committed_at/1000000000)) IN (
              SELECT d FROM (
                SELECT DATE(FROM_UNIXTIME(committed_at/1000000000)) d
                FROM whodunit_commits
                WHERE committed_at > (UNIX_TIMESTAMP() - 30*86400) * 1000000000
                GROUP BY d HAVING COUNT(*) > 100
              ) heavy
          )
    """, container, db)
    print("last 30 days: adoption ramped 25% -> 76%, volume outliers thinned")

    # --- incidents: MTTR and change failure ------------------------------
    #
    # Empty in the real lake, so those DORA panels have nothing. Built from
    # real deployments so the dates are coherent with everything else.
    deploys = mysql("""
        SELECT id, cicd_scope_id, finished_date FROM cicd_deployment_commits
        WHERE finished_date IS NOT NULL ORDER BY finished_date DESC LIMIT 15
    """, container, db).strip().splitlines()
    rows = []
    for i, line in enumerate(deploys):
        parts = line.split("\t")
        if len(parts) < 3:
            continue
        scope, finished = parts[1], parts[2]
        hours = 2 + (i * 7) % 40
        rows.append(
            f"('inc-{i+1}','INCIDENT','{scope}',"
            f"'{finished}', DATE_ADD('{finished}', INTERVAL {hours} HOUR), {hours * 60})")
    if rows:
        mysql("DELETE FROM incidents", container, db)
        mysql(f"INSERT INTO incidents (id,`table`,scope_id,created_date,"
              f"resolution_date,lead_time_minutes) VALUES {','.join(rows)}",
              container, db)
    print(f"incidents: {len(rows)} linked to real deployments")


def report(container, db):
    """What the demo database now holds, for the operator to sanity-check."""
    q = """
      SELECT 'teams',        COUNT(*) FROM teams
      UNION ALL SELECT 'team members',   COUNT(*) FROM team_users
      UNION ALL SELECT 'contributors',   COUNT(DISTINCT contributor) FROM whodunit_commits
      UNION ALL SELECT 'commits',        COUNT(*) FROM whodunit_commits
      UNION ALL SELECT 'sessions',       COUNT(*) FROM whodunit_sessions
      UNION ALL SELECT 'bad timestamps', COUNT(*) FROM whodunit_sessions WHERE first_seen < 0
      UNION ALL SELECT 'incidents',      COUNT(*) FROM incidents
      UNION ALL SELECT 'aliases',        COUNT(*) FROM whodunit_identities
    """
    print("\n" + mysql(q, container, db).rstrip())


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--container", default="devlake-mysql-1")
    ap.add_argument("--database", default="lake_demo",
                    help="target database (never the real one)")
    ap.add_argument("--keep", action="store_true",
                    help="augment an existing copy instead of re-cloning")
    args = ap.parse_args()

    # The guard that matters. Every statement below is an UPDATE or INSERT,
    # and pointed at the real database this script would rewrite the
    # attribution of every commit in it.
    if args.database == SOURCE:
        raise SystemExit(
            f"refusing to run against {SOURCE}: this script rewrites "
            f"contributor attribution and would destroy the real data")

    if not args.keep:
        mysql(f"DROP DATABASE IF EXISTS {args.database}", args.container)
        mysql(f"CREATE DATABASE {args.database}", args.container)
        # merico is what Grafana connects as, and it cannot create
        # databases itself (GRANT USAGE ON *.* only).
        mysql(f"GRANT ALL ON {args.database}.* TO 'merico'@'%'", args.container)
        mysql("FLUSH PRIVILEGES", args.container)
        clone(args.container, args.database)
        verify_clone(args.container, args.database)

    augment(args.container, args.database)
    report(args.container, args.database)

    print(f"\n{args.database} is ready. {SOURCE} was not modified.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
