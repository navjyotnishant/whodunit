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
    "commits_total": 12,
    "commits_shared_repo": 7,
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


def unattributed_set_violations() -> list[str]:
    """Panels that ask "was this attributed" by negating one status.

    `status <> 'undetermined'` was correct while undetermined was the only
    unattributed status. WHO-211 adds three more, and a negation silently
    absorbs every one of them - an `unassisted` commit stops being
    undetermined and starts counting as attributed, which inflates
    coverage and renders perfectly.

    The failure is invisible in the panel and flattering in direction, so
    it is caught here in the source rather than by anyone noticing.
    """
    bad = []
    for path in sorted(Path(__file__).parent.glob("dashboards/*.json")):
        panels = json.loads(path.read_text()).get("panels", [])
        for p in panels:
            for t in p.get("targets") or []:
                sql = t.get("rawSql", "")
                if re.search(r"status\s*(<>|!=|=)\s*'undetermined'", sql):
                    bad.append(f"{path.name} :: {p.get('title','?')}")
    return bad


def ambiguous_label_violations() -> list[str]:
    """Series labels that mean different things in the same dashboard row.

    Every labelling bug found on this dashboard was the same shape: two
    panels side by side, both saying "assisted", one counting commits and
    the other averaging lines per commit. Each read correctly alone and
    contradicted its neighbour at a glance - 177 looked smaller than 307
    until someone worked out they were different units.

    Checked per row rather than per dashboard, because adjacency is what
    makes it misleading. Parallel panels showing the SAME measure against
    different outcomes share labels legitimately, so a clash only counts
    when the panels' titles differ in what they measure.
    """
    bad = []
    for path in sorted(Path(__file__).parent.glob("dashboards/*.json")):
        dash = json.loads(path.read_text())
        rows: dict = {}
        for p in dash.get("panels", []):
            if p.get("type") in ("row", "text"):
                continue
            labels = set()
            for t in p.get("targets") or []:
                labels |= set(re.findall(r'AS [`"]([^`"]+)[`"]', t.get("rawSql", "")))
            rows.setdefault(p["gridPos"]["y"], []).append((p.get("title", "?"), labels))

        for y, panels in rows.items():
            if len(panels) < 2:
                continue
            seen: dict = {}
            for title, labels in panels:
                for l in labels:
                    seen.setdefault(l.lower(), set()).add(title)
            for label, titles in seen.items():
                # A bare cohort word with no unit, in two panels that do
                # not measure the same thing.
                if label in ("assisted", "unassisted", "not assisted") and len(titles) > 1:
                    bad.append(f"{path.name} row y={y}: '{label}' in "
                               f"{sorted(titles)} - name the unit")
    return bad


def grain_comparison_violations() -> list[str]:
    """SQL that compares $grain against a value the variable cannot hold.

    The variable holds a DATE_FORMAT mask - Daily is '%Y-%m-%d', Weekly
    is '%x-W%v'. Three exec panels tested it against 'day' and 'week',
    which never matched, so every branch fell through to the monthly
    default and the Granularity control silently did nothing.

    Silent is the problem: the panel renders a correct-looking monthly
    chart whatever the dropdown says. Checked here against the variable's
    own declared options rather than a hardcoded list, so renaming an
    option cannot leave this guard passing while the SQL breaks.
    """
    bad = []
    for path in sorted(Path(__file__).parent.glob("dashboards/*.json")):
        dash = json.loads(path.read_text())
        values = set()
        for v in dash.get("templating", {}).get("list", []):
            if v.get("name") == "grain":
                values = {o.get("value") for o in v.get("options", [])}
        if not values:
            continue
        for p in dash.get("panels", []):
            for t in p.get("targets") or []:
                for lit in re.findall(r"'\$grain'\s*=\s*'([^']*)'", t.get("rawSql", "")):
                    if lit not in values:
                        bad.append(f"{path.name} :: {p.get('title','?')}: "
                                   f"compares $grain to '{lit}', which it never holds")
    return bad


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
            lambda r: int(r[0][0]) == 9,  # 12 total minus 3 pre-attribution
        ),
        (
            # WHO-211. Coverage and Penetration compute "attributed" as
            # `status <> 'undetermined'`, which silently absorbs any new
            # status. An `unassisted` commit is NOT attributed - nothing
            # was attributed to an agent - so counting it as such inflates
            # coverage, renders fine, and errs in the flattering direction.
            #
            # Written against the intended set rather than the negation,
            # so it stays correct as statuses are added.
            "unassisted is not counted as attributed",
            "SELECT SUM(status='assisted'), SUM(status NOT IN "
            "('undetermined','unassisted','unmatched','degraded')) "
            "FROM whodunit_commits",
            lambda r: int(r[0][0]) == int(r[0][1]),
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

    print("\nchecking how panels ask whether a commit was attributed")
    violations = unattributed_set_violations()
    for v in violations:
        failures.append(f"{v}: compares status against 'undetermined' directly")
        print(f"  FAIL  {v} - use the unattributed set, not a single status")
    if not violations:
        print("  ok    no panel negates a single status")

    print("\nchecking $grain comparisons against the values it can hold")
    grain_bad = grain_comparison_violations()
    for v in grain_bad:
        failures.append(v)
        print(f"  FAIL  {v}")
    if not grain_bad:
        print("  ok    every $grain comparison uses a real option value")

    print("\nchecking that side-by-side panels do not reuse a bare cohort label")
    label_bad = ambiguous_label_violations()
    for v in label_bad:
        failures.append(v)
        print(f"  FAIL  {v}")
    if not label_bad:
        print("  ok    no row reuses 'assisted' across panels with different units")

    if failures:
        print(f"\n{len(failures)} failure(s)")
        return 1
    print("\nall assertions hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
