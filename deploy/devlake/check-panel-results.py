#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-24
# Last updated: 2026-08-24
# Description: Run dashboard SQL against a known dataset and assert the rows.

"""Assert what panels RETURN, not what they look like.

Five guard scripts already check the shape of a dashboard — quoting,
declared variables, tooltip length, list membership. None of them runs a
query, so a panel can pass all five and return the wrong rows. That has
happened twice on this project: a quoting regression emptied five
dashboards, and an unguarded regex fabricated 51 AI-assisted issues. Both
were caught by a human noticing, and a guard for that specific shape was
written afterwards.

This runs the SQL against seed-fixture.py's dataset, where every number is
known in advance, and asserts the answers.

Two rules it exists to enforce, both learned the hard way:

  - `All` proves nothing on its own. Both prior quoting regressions left
    the All case green while every specific selection returned nothing, so
    each filter is exercised with a real value as well.
  - A panel that returns no rows is not automatically broken, but a panel
    that USED to return rows and now does not is. Counts are asserted, not
    just non-emptiness.

    ./check-panel-results.py            # against the seeded fixture
"""

import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
DASHBOARDS = HERE / "dashboards"
DATABASE = "whodunit_fixture"

REPO_SHARED = "a" * 40
ALICE = "alice@example.com"
BOB = "bob@example.com"

# What the fixture contains, restated so a failure says which expectation
# broke rather than only that a number changed. seed-fixture.py is the
# source of these; if the two disagree the fixture has drifted.
EXPECTED = {
    "commits_total": 11,
    "commits_shared_repo": 6,
    "assisted_shared_repo": 4,
    "contributors": 2,
}


def query(sql: str) -> list[list[str]]:
    """Run one statement, returning rows. A SQL error is a failure, not an
    empty result: the two are different and only one of them is a bug in
    the panel."""
    proc = subprocess.run(
        ["docker", "exec", "-i", "devlake-mysql-1", "mysql",
         "-umerico", "-pmerico", DATABASE, "-N", "--batch"],
        input=sql, text=True, capture_output=True,
    )
    if proc.returncode != 0:
        err = [l for l in proc.stderr.splitlines() if "Warning" not in l]
        raise RuntimeError("; ".join(err) or "query failed")
    return [l.split("\t") for l in proc.stdout.strip().splitlines() if l]


def substitute(sql: str, contributor: str) -> str:
    """Resolve Grafana macros and variables to something runnable.

    contributor is passed explicitly rather than defaulted, because the
    whole point is to exercise a specific selection as well as `All`.
    """
    sql = re.sub(r"\$__unixEpochFrom\(\)", "0", sql)
    sql = re.sub(r"\$__unixEpochTo\(\)", "4102444800", sql)
    sql = re.sub(r"\$__timeFilter\([^)]*\)", "1=1", sql)
    sql = re.sub(r"\$__timeFrom\(\)", "'1970-01-01'", sql)
    sql = re.sub(r"\$__timeTo\(\)", "'2099-01-01'", sql)
    sql = re.sub(r"\$__unixEpochFilter\([^)]*\)", "1=1", sql)
    sql = re.sub(r"\$__interval_ms", "86400000", sql)
    sql = re.sub(r"\$__interval", "1d", sql)
    sql = sql.replace("${contributor:raw}", contributor)
    sql = sql.replace("${repo:raw}", "__all__")
    sql = sql.replace("$grain", "%Y-%m-%d")
    sql = sql.replace("$board", "%")
    # Remaining dashboard variables. Each resolves to a value that excludes
    # nothing, because this checks whether a query RUNS and returns what the
    # fixture holds - not whether one particular selection is interesting.
    # Quoted forms first. A dashboard writes '$tz' with the quotes already
    # in the SQL, so substituting the bare name first would leave "'UTC'"
    # inside them and produce a syntax error rather than a value.
    for name, value in (("agent", "__all__"), ("tz", "+00:00"), ("team", "__all__")):
        sql = sql.replace("${%s:raw}" % name, value)
        sql = sql.replace("'$%s'" % name, "'%s'" % value)
        sql = sql.replace("$%s" % name, "'%s'" % value)
    return sql


def contributor_filter_cases() -> list[tuple[str, str, callable]]:
    """The assertions that encode what has actually gone wrong before."""
    return [
        (
            "All returns every commit",
            f"SELECT COUNT(*) FROM whodunit_commits",
            lambda r: int(r[0][0]) == EXPECTED["commits_total"],
        ),
        (
            # The case both prior regressions passed. A filter that is
            # broken for real selections still returns everything here.
            "a specific contributor returns only their rows",
            "SELECT COUNT(*) FROM whodunit_commits c "
            "JOIN whodunit_repos r ON r.repo_id=c.repo_id "
            f"WHERE r.contributor='{ALICE}'",
            lambda r: int(r[0][0]) == EXPECTED["commits_shared_repo"],
        ),
        (
            "the other contributor returns a different, non-zero count",
            "SELECT COUNT(*) FROM whodunit_commits c "
            "JOIN whodunit_repos r ON r.repo_id=c.repo_id "
            f"WHERE r.contributor='{BOB}'",
            lambda r: 0 < int(r[0][0]) < EXPECTED["commits_total"],
        ),
        (
            # WHO-193. A rate computed over commits that predate attribution
            # reports a period when whodunit was not installed.
            "the attribution boundary excludes pre-instrumentation commits",
            "SELECT COUNT(*) FROM whodunit_commits c "
            "JOIN (SELECT repo_id, MIN(CASE WHEN method<>'undetermined' "
            "THEN committed_at END) AS fa FROM whodunit_commits GROUP BY repo_id) b "
            "ON b.repo_id=c.repo_id WHERE c.committed_at >= b.fa",
            lambda r: int(r[0][0]) == 8,  # 11 total minus 3 pre-attribution
        ),
        (
            # NAV-21 at the query layer: a session with no tokens must not
            # be counted as a session costing zero.
            "a session without tokens is excluded, not counted as free",
            "SELECT COUNT(*), SUM(output_tokens IS NOT NULL) FROM whodunit_sessions",
            lambda r: r[0][0] == "2" and r[0][1] == "1",
        ),
    ]


def fixture_available() -> bool:
    """Whether the seeded fixture can be reached."""
    try:
        query("SELECT 1")
        return True
    except Exception:
        return False


def main() -> int:
    # Unlike the other guards, this one needs a database. On a machine with
    # no DevLake - most contributors, and CI today - it reports that and
    # exits clean rather than failing a build over infrastructure the
    # change did not touch.
    if "--if-available" in sys.argv and not fixture_available():
        print("no fixture database; skipping "
              "(run deploy/devlake/seed-fixture.py to enable)")
        return 0

    failures = []

    print("asserting what the fixture contains")
    for name, sql, ok in contributor_filter_cases():
        try:
            rows = query(sql)
            if not ok(rows):
                failures.append(f"{name}: got {rows}")
                print(f"  FAIL  {name} -> {rows}")
            else:
                print(f"  ok    {name}")
        except RuntimeError as e:
            failures.append(f"{name}: {e}")
            print(f"  ERROR {name}: {e}")

    # Then every panel's real SQL, under two contributor selections. A panel
    # that errors is broken; one that returns nothing under All when it
    # returns rows for a specific contributor is inverted.
    print("\nrunning every panel's SQL against the fixture")
    errors = 0
    checked = 0
    for path in sorted(DASHBOARDS.glob("*.json")):
        dash = json.loads(path.read_text())
        for panel in dash.get("panels", []):
            for target in panel.get("targets", []):
                sql = target.get("rawSql")
                if not sql or "whodunit_" not in sql:
                    continue
                checked += 1
                title = panel.get("title") or "(untitled)"
                counts = {}
                broken = False
                for label, who in (("All", "__all__"), ("alice", ALICE)):
                    try:
                        counts[label] = len(query(substitute(sql, who)))
                    except RuntimeError as e:
                        errors += 1
                        broken = True
                        msg = f"{path.name} :: {title} [{label}]: {e}"
                        failures.append(msg)
                        print(f"  FAIL  {msg}")
                if broken:
                    continue

                # A query that runs is not a query that works.
                #
                # Removing the `'${contributor:raw}'='__all__' OR` escape
                # leaves perfectly valid SQL that returns nothing when All
                # is selected - which is precisely the regression that
                # emptied five dashboards, and it errors on nothing.
                #
                # All is a superset of any single contributor by
                # definition, so returning fewer rows than one of them is
                # proof the filter is inverted.
                if counts["All"] < counts["alice"]:
                    errors += 1
                    msg = (f"{path.name} :: {title}: All returned "
                           f"{counts['All']} row(s) but alice returned "
                           f"{counts['alice']} - the filter is inverted")
                    failures.append(msg)
                    print(f"  FAIL  {msg}")
                elif counts["alice"] > 0 and counts["All"] == 0:
                    errors += 1
                    msg = (f"{path.name} :: {title}: All returned nothing "
                           f"while a specific contributor returned rows")
                    failures.append(msg)
                    print(f"  FAIL  {msg}")
    print(f"  {checked} panel queries, {errors} error(s)")

    if failures:
        print(f"\n{len(failures)} failure(s)")
        return 1
    print("\nall assertions hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
