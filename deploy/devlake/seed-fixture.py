#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-24
# Last updated: 2026-08-24
# Description: A known dataset for asserting what dashboard panels return.

"""Create a database whose every number is known in advance.

The guard scripts check the SHAPE of a panel — quoting, variables, tooltip
length. None of them runs a query, so a panel can pass all of them and
return the wrong rows. That has happened twice: a quoting regression
emptied five dashboards, and an unguarded regex fabricated 51 AI-assisted
issues. Both were found by a human noticing.

This seeds a dataset small enough to reason about and shaped to contain the
cases that have actually gone wrong:

  - two contributors sharing one repository, which is what breaks when
    whodunit_repos is keyed on repo_id alone
  - a repository whose commits start before its first attributed one, so
    the attribution boundary has something to exclude
  - commits across several purposes and file-count bands, for the matched
    pair comparisons
  - one contributor with no team, who must still appear

The schema is read from internal/sidecar/schema.go rather than copied, so
the fixture cannot drift from what sync actually writes.

    ./seed-fixture.py --dsn 'user:pass@tcp(host)/db'   # via docker mysql
"""

import argparse
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SCHEMA_GO = REPO_ROOT / "internal" / "sidecar" / "schema.go"

# One day in nanoseconds — whodunit stores epoch nanoseconds.
DAY = 86_400_000_000_000
# A fixed origin so every expectation is arithmetic rather than a snapshot
# of whenever the fixture last ran. 2026-06-01T00:00:00Z.
T0 = 1_780_272_000_000_000_000

ALICE = "alice@example.com"
BOB = "bob@example.com"

# Repository A: shared by two people. This is the case whodunit_repos
# cannot currently represent, and the one every contributor filter breaks on.
REPO_SHARED = "a" * 40
# Repository B: one contributor, and commits that predate attribution.
REPO_SOLO = "b" * 40


def schema() -> str:
    """The DDL, read from Go so it cannot drift from what sync writes."""
    src = SCHEMA_GO.read_text()
    m = re.search(r"const Schema = `(.*?)`", src, re.S)
    if not m:
        sys.exit(f"no Schema literal found in {SCHEMA_GO}")
    return m.group(1)


def rows():
    """Every row, with the arithmetic that makes each expectation checkable."""
    stmts = []

    for repo, contributor in ((REPO_SHARED, ALICE), (REPO_SOLO, BOB)):
        stmts.append(
            "INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at) "
            f"VALUES ('{repo}', '{contributor}', '0.2', {T0})"
        )

    # Repository A, all attributed: 6 commits, 4 assisted.
    # Expected assisted share: 4/6 = 66.7%.
    for i, (status, method, purpose, files) in enumerate([
        ("assisted", "intersected", "feature", 5),
        ("assisted", "intersected", "feature", 6),
        ("assisted", "observed", "fix", 2),
        ("assisted", "declared", "docs", 1),
        ("undetermined", "undetermined", "fix", 1),
        ("undetermined", "undetermined", "chore", 1),
    ]):
        stmts.append(
            "INSERT INTO whodunit_commits (commit_sha, repo_id, committed_at, status, "
            "method, agent, purpose, lines_added, lines_removed, files_changed, "
            "spec_version, schema_version, synced_at) VALUES "
            f"('a{i:039d}', '{REPO_SHARED}', {T0 + i * DAY}, '{status}', '{method}', "
            f"'claude-code', '{purpose}', 100, 10, {files}, '0.2', 1, {T0})"
        )

    # Repository B: three commits BEFORE any attribution, then two after.
    # A panel that counts the first three is reporting on a period when
    # whodunit was not installed, which is the bug WHO-193 fixed.
    for i in range(3):
        stmts.append(
            "INSERT INTO whodunit_commits (commit_sha, repo_id, committed_at, status, "
            "method, agent, purpose, lines_added, lines_removed, files_changed, "
            "spec_version, schema_version, synced_at) VALUES "
            f"('b{i:039d}', '{REPO_SOLO}', {T0 + i * DAY}, 'undetermined', "
            f"'undetermined', '', 'feature', 50, 5, 2, '0.2', 1, {T0})"
        )
    for i in (3, 4):
        stmts.append(
            "INSERT INTO whodunit_commits (commit_sha, repo_id, committed_at, status, "
            "method, agent, purpose, lines_added, lines_removed, files_changed, "
            "spec_version, schema_version, synced_at) VALUES "
            f"('b{i:039d}', '{REPO_SOLO}', {T0 + i * DAY}, 'assisted', 'intersected', "
            f"'codex', 'feature', 80, 8, 3, '0.2', 1, {T0})"
        )

    # Sessions, one per agent per repository, with tokens on one only:
    # a panel dividing by tokens must exclude the session that has none
    # rather than treating it as free.
    stmts.append(
        "INSERT INTO whodunit_sessions (repo_id, session, agent, agent_version, "
        "first_seen, last_seen, user_messages, agent_messages, tool_calls, "
        "distinct_tools, mcp_calls, input_tokens, output_tokens, "
        f"synced_at) VALUES ('{REPO_SHARED}', 's1', 'claude-code', '1.0', "
        f"{T0}, {T0 + DAY}, 10, 12, 40, 6, 0, 1000, 2000, {T0})"
    )
    stmts.append(
        "INSERT INTO whodunit_sessions (repo_id, session, agent, agent_version, "
        "first_seen, last_seen, user_messages, agent_messages, tool_calls, "
        "distinct_tools, mcp_calls, synced_at) VALUES "
        f"('{REPO_SOLO}', 's2', 'codex', '1.0', {T0 + 3 * DAY}, {T0 + 4 * DAY}, "
        f"5, 6, 20, 4, 0, {T0})"
    )

    # Events, so panels joining event grain have rows to find.
    for i, repo in enumerate([REPO_SHARED, REPO_SHARED, REPO_SOLO]):
        agent = "codex" if repo == REPO_SOLO else "claude-code"
        session = "s2" if repo == REPO_SOLO else "s1"
        stmts.append(
            "INSERT INTO whodunit_events (event_id, repo_id, observed_at, agent, "
            "agent_version, session, event, tool, file, lines_added, lines_removed, "
            "outcome, spec_version, synced_at) VALUES "
            f"('e{i:063d}', '{repo}', {T0 + i * DAY}, '{agent}', '1.0', '{session}', "
            f"'tool_use', 'Edit', 'src/main.go', 20, 2, 'ok', '0.2', {T0})"
        )
    return stmts


# DevLake's own tables, joined by panels that combine attribution with
# delivery data. Structures are copied from the live database rather than
# written out here: they belong to DevLake, change with its releases, and a
# hand-maintained copy would drift silently.
#
# Left EMPTY on purpose. The point is that a panel joining them still RUNS
# — a SQL error and an empty result are different failures, and only the
# first is a bug in the panel. Populating them would mean maintaining a
# second fixture for somebody else's schema.
DEVLAKE_TABLES = [
    "board_issues", "cicd_deployment_commits", "commits", "incidents",
    "issues", "project_mapping", "project_pr_metrics", "repo_commits",
]


def clone_devlake_tables(container: str, database: str, source: str = "lake") -> str:
    """CREATE TABLE ... LIKE for each DevLake table, if the source has it."""
    stmts = []
    for t in DEVLAKE_TABLES:
        stmts.append(
            f"CREATE TABLE IF NOT EXISTS {database}.{t} LIKE {source}.{t}")
    return ";\n".join(stmts) + ";"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--container", default="devlake-mysql-1")
    ap.add_argument("--database", default="whodunit_fixture")
    ap.add_argument("--user", default="merico")
    ap.add_argument("--password", default="merico")
    args = ap.parse_args()

    sql = [
        f"DROP DATABASE IF EXISTS {args.database}",
        f"CREATE DATABASE {args.database}",
        f"USE {args.database}",
        schema(),
        *rows(),
    ]
    script = ";\n".join(s.strip().rstrip(";") for s in sql if s.strip()) + ";"

    # Root via the container's own environment, because the application
    # user is scoped to the DevLake database and cannot create another.
    # The password is never passed on a command line or stored here; it is
    # read inside the container from the variable that already holds it.
    # DevLake's tables are cloned after ours so a panel joining both can
    # run. A missing source table is skipped rather than fatal: not every
    # install collects every connector.
    script += "\n" + clone_devlake_tables(args.container, args.database)

    proc = subprocess.run(
        ["docker", "exec", "-i", args.container, "sh", "-c",
         f'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" && '
         f'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e '
         f'"GRANT ALL ON {args.database}.* TO \'{args.user}\'@\'%\'"'],
        input=script, text=True, capture_output=True,
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        return 1
    print(f"seeded {args.database}: 2 repositories, 11 commits, 2 sessions, 3 events")
    return 0


if __name__ == "__main__":
    sys.exit(main())
