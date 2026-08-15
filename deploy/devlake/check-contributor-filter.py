#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Fails when a dashboard's contributor variable hides
# repositories with no recorded identity.
"""Check that the contributor filter cannot silently hide data.

The original variable query was:

    SELECT DISTINCT contributor FROM whodunit_repos WHERE contributor <> ''

which excludes repositories whose contributor was never recorded. Those
rows are then unreachable by any filter value AND absent from the
dropdown, so the data is synced and invisible. Measured before the fix,
115 of 131 sessions were in that state (NAV-110).

An empty contributor is a legitimate state — a repository instrumented
before metadata existed, or on a machine with no git identity configured —
so the answer is to make it selectable, not to assume it away.
"""

import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
BAD = "WHERE contributor <> ''"


def main():
    problems = []
    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))

        # Grafana's MySQL datasource quotes an interpolated variable
        # itself, so '$contributor' in the SQL becomes ''value'' — a
        # syntax error that fails the panel with a 400.
        #
        # This one hid behind the "All" case for a long time: Grafana
        # substitutes the all-value as a bare __all__ token rather than a
        # quoted string, so the dashboard worked perfectly until someone
        # picked a real contributor, and then every filtered panel errored
        # at once.
        for p in d.get("panels", []):
            for t in p.get("targets", []):
                if "'$contributor'" in (t.get("rawSql") or ""):
                    problems.append(
                        f"{d['uid']}: {p.get('title') or '(untitled)'} wraps "
                        f"$contributor in quotes; Grafana quotes it too, "
                        f"producing ''value'' and a 400")
        for v in d.get("templating", {}).get("list", []):
            if v.get("name") != "contributor":
                continue
            q = v.get("query", "")
            if BAD in q:
                problems.append(
                    f"{d['uid']}: contributor variable excludes empty values, so "
                    f"a repository with no recorded identity is unreachable")
            elif "COALESCE" not in q and "NULLIF" not in q:
                problems.append(
                    f"{d['uid']}: contributor variable does not offer an "
                    f"option for repositories with no recorded identity")
            elif v.get("includeAll") and v.get("multi") is not False:
                # With includeAll set and multi absent, Grafana treats the
                # variable as multi-capable and substitutes a chosen value
                # as {value}. That matches nothing, so every panel empties
                # the moment a contributor is selected — while "All" keeps
                # working, because __all__ is compared literally and never
                # braced. The filter looks functional and silently returns
                # no rows.
                problems.append(
                    f"{d['uid']}: contributor variable has includeAll without "
                    f"multi=false, so a selected value is substituted as "
                    f"{{value}} and matches nothing")
            elif q.upper().count("ORDER BY") > 1:
                # A malformed query fails silently: Grafana renders an
                # empty dropdown rather than an error, so the filter looks
                # present and offers nothing. This one was introduced by a
                # search-and-replace that appended ORDER BY to a query
                # that already had one.
                problems.append(
                    f"{d['uid']}: contributor variable has two ORDER BY "
                    f"clauses and is not valid SQL — the dropdown will be "
                    f"empty with no error shown")

    if problems:
        print("contributor filter would hide data:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print("contributor filter reaches every repository")
    return 0


if __name__ == "__main__":
    sys.exit(main())
