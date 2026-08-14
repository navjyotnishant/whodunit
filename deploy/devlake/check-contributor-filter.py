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

    if problems:
        print("contributor filter would hide data:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print("contributor filter reaches every repository")
    return 0


if __name__ == "__main__":
    sys.exit(main())
