#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-16
# Last updated: 2026-08-16
# Description: Fails when a panel matches issue keys against commit
# messages without constraining the key's shape.
"""Check that no panel can attribute a commit to the wrong issue.

Panels link an issue to its commits by searching the commit message for
the issue key:

    LEFT JOIN commits c
      ON c.message REGEXP CONCAT('(^|[^A-Za-z0-9])', i.issue_key, '([^0-9]|$)')

That works for a tracker whose keys look like PROJ-123. It does not work
for GitHub, whose issue keys are bare integers: issue `1` matches any
commit message containing an isolated 1, so unrelated commits are
attributed to it and their AI attribution comes with them.

Measured on the boards in one real database (NAV-122), with the guard
absent and then present:

    navjyotnishant/specter-agent    11 -> 0
    navjyotnishant/nj-agents         4 -> 0
    navjyotnishant/whodunit          2 -> 0

Every one of those was a fabricated AI-assisted issue. Nothing errored;
the panels rendered a plausible number. Linear boards are unaffected
(1 -> 1), because their keys carry the project prefix the guard requires.

The dashboards were correct only because the board variable happened to
be scoped to Linear. That is a property of a dropdown's default, not of
the query, and selecting a GitHub board silently published the fabricated
counts.

Checked statically because the failure is invisible at runtime: there is
no error to notice, only a wrong number that looks like a right one.
"""

import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# The join that needs the guard: an issue key matched against commit text.
# Counted per occurrence, not searched once — a panel can carry several.
JOIN = re.compile(r"message\s+REGEXP", re.IGNORECASE)

# The guard itself. Matched loosely on the distinctive character class so
# reformatting the SQL does not trip the check.
GUARD = re.compile(r"issue_key\s+REGEXP\s+'\^\[A-Z\]\[A-Z0-9\]\+-\[0-9\]\+\$'")


def main():
    problems = []
    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))
        for p in d.get("panels", []):
            for t in p.get("targets", []):
                sql = t.get("rawSql") or ""
                joins = len(JOIN.findall(sql))
                if not joins:
                    continue
                guards = len(GUARD.findall(sql))
                if guards >= joins:
                    continue
                # Counted rather than merely searched. A panel that
                # subtracts one cohort from another carries the join
                # twice, and guarding only the first is worse than
                # guarding neither: it subtracts a correct figure from a
                # fabricated one, so the error survives as a plausible
                # delta instead of an obvious one. The Delta panel
                # shipped in exactly that state (NAV-122).
                problems.append(
                    f"{d['uid']}: {p.get('title') or '(untitled)'} matches "
                    f"issue keys against commit messages in {joins} place(s) "
                    f"but guards {guards}; every one needs "
                    f"AND i.issue_key REGEXP '^[A-Z][A-Z0-9]+-[0-9]+$' or, on "
                    f"a tracker with bare-integer keys, it attributes "
                    f"unrelated commits and reports AI-assisted issues that "
                    f"do not exist")

        # A panel that filters by $board on a dashboard that never declares
        # the variable substitutes an undefined value. Grafana does not
        # error: it sends the literal text, the query matches nothing, and
        # the panel renders empty as though the data were absent.
        declared = {v.get("name") for v in d.get("templating", {}).get("list", [])}
        for p in d.get("panels", []):
            for t in p.get("targets", []):
                sql = t.get("rawSql") or ""
                if "$board" in sql and "board" not in declared:
                    problems.append(
                        f"{d['uid']}: {p.get('title') or '(untitled)'} filters "
                        f"on $board but the dashboard declares no board "
                        f"variable, so the panel renders empty rather than "
                        f"reporting an error")

    if problems:
        print("issue-key matching would fabricate attribution:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print("issue keys are matched with a project-prefix guard")
    return 0


if __name__ == "__main__":
    sys.exit(main())
