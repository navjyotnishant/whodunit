#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-16
# Last updated: 2026-08-25
# Description: Fails when a panel joins issues to commits without
# constraining the issue key's shape.
"""Check that no panel can attribute a commit to the wrong issue.

Panels link an issue to its commits one of two ways. The original join
searched the commit message for the issue key:

    LEFT JOIN commits c
      ON c.message REGEXP CONCAT('(^|[^A-Za-z0-9])', i.issue_key, '([^0-9]|$)')

The panels now use the collector's own mapping instead, which is far
cheaper and cannot invent a pair (WHO-216):

    LEFT JOIN issue_commits c ON c.issue_id = i.id

Both are checked, because the guard is what makes the first one safe and
what will keep the second one safe if a connector ever populates
issue_commits for a bare-integer board.

The text join works for a tracker whose keys look like PROJ-123. It does not work
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

# Two joins link an issue to its commits, and both need the guard.
#
# The text join matches the key against the commit message. It is the one
# that fabricates attribution on a bare-integer tracker, and it is also
# slow: it scans every commit per issue, which is what made the cycle
# panels time out once both cohorts carried data (WHO-216).
#
# The foreign-key join reads issue_commits, the collector's own mapping.
# It is ~30x faster and was measured equivalent on a real database. It
# cannot fabricate a pair the way the text join can, so the guard is
# currently inert there — but it is kept and still checked, because
# nothing stops a connector from populating issue_commits for a
# bare-integer board later, and a guard removed as "unnecessary" is not
# there on the day that changes.
#
# Counted per occurrence, not searched once — a panel can carry several.
JOIN = re.compile(r"message\s+REGEXP|JOIN\s+issue_commits\b", re.IGNORECASE)

# The guard itself. Matched loosely on the distinctive character class so
# reformatting the SQL does not trip the check.
#
# Anchored to "i.issue_key" — the outer issue being joined. The same regex
# literal also appears inside the board-prefix subquery, on a different
# alias (i2), and counting those inflated the total: every panel scored
# two guards per join, so a real guard could be deleted and the count
# still cleared. Only the outer occurrence defends the join.
GUARD = re.compile(
    r"\bi\.issue_key\s+REGEXP\s+'\^\[A-Z\]\[A-Z0-9\]\+-\[0-9\]\+\$'")


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
                    f"{d['uid']}: {p.get('title') or '(untitled)'} joins "
                    f"issues to commits in {joins} place(s) but guards "
                    f"{guards}; every one needs "
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
