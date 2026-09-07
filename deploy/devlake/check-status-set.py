#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-25
# Last updated: 2026-08-25
# Description: Fails when a panel's status set omits a status the database
# actually holds.
"""Check that no panel silently misclassifies a status it has never heard of.

A panel that splits commits into "attributed" and "not attributed" writes
the split as a set membership:

    SUM(status IN ('assisted','unassisted','unmatched'))
    SUM(status NOT IN ('undetermined','unassisted','unmatched','degraded'))

Every status the database holds has to appear in that set, or on the side
the panel means it to fall. A status the set does not mention is not an
error: MySQL evaluates the comparison, the row lands on whichever side the
omission happens to put it, and the panel renders a plausible number.

This already happened once. WHO-211 predicted it — "a four-value set every
future query must remember, where forgetting yields a plausible wrong
number rather than an error" — and WHO-218 is the instance: `uninstrumented`
arrived as a fifth status, no set was revisited, and Coverage counted 162
commits the tool was never present for as attributed. It read 40.2% where
the honest figure over the same query was 25.1%, in the flattering
direction, on a panel whose own tooltip says every other number depends on
it.

Checked against the STATUSES list below rather than against the live
database, so the check is deterministic and runs without a container. When
`dun` learns a sixth status, this list is the one place that has to change,
and every panel that forgot it fails here.
"""

import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Every status `dun` can write. internal/spec/trailer.go is the source of
# truth; this mirrors it.
STATUSES = {
    "assisted",
    "unassisted",
    "unmatched",
    "uninstrumented",
    "undetermined",
    "degraded",
}

# The five that mean something other than "an agent wrote this". A panel
# splitting attributed from unattributed reasons about these, and naming
# some but not all of them is the bug: the unnamed one silently joins the
# other side. `assisted` is deliberately not required — a set may name it
# or exclude it, but it is never the value that gets forgotten.
NON_ASSISTED = STATUSES - {"assisted"}

# `status IN (...)` / `status NOT IN (...)`, capturing the quoted values.
STATUS_SET = re.compile(r"status\s+(?:NOT\s+)?IN\s*\(([^)]*)\)", re.IGNORECASE)
QUOTED = re.compile(r"'([a-z_]+)'")

# Some sets leave a status out on purpose — Penetration excludes the states
# meaning the tool never looked, because counting them would understate a
# rate rather than complete it. A deliberate exclusion carries this marker
# in the SQL naming the statuses it drops, so the check can tell a decision
# from an oversight. Without it, every intentional exclusion would have to
# be silenced by weakening the rule for everyone.
DELIBERATE = re.compile(
    r"--\s*status-set-excludes:\s*([a-z_,\s]+)", re.IGNORECASE)


def main():
    problems = []
    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))
        for p in d.get("panels", []):
            for t in p.get("targets", []):
                sql = t.get("rawSql") or ""
                for m in STATUS_SET.finditer(sql):
                    named = set(QUOTED.findall(m.group(1)))
                    if not named or not (named & NON_ASSISTED):
                        # A set that reasons only about `assisted` is not
                        # making the attributed/unattributed split this
                        # check is about.
                        continue
                    excused = set()
                    for e in DELIBERATE.findall(sql):
                        excused |= {x.strip() for x in e.split(",") if x.strip()}
                    missing = NON_ASSISTED - named - excused
                    if not missing:
                        continue
                    problems.append(
                        f"{d['uid']}: {p.get('title') or '(untitled)'} splits on "
                        f"status IN ({', '.join(sorted(named))}) but the database "
                        f"can also hold {', '.join(sorted(missing))}; those rows "
                        f"land on whichever side the omission happens to put "
                        f"them, and the panel renders a number that looks right")

    if problems:
        print("a panel's status set omits a status dun can write:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print(f"every status set accounts for all {len(NON_ASSISTED)} non-assisted statuses")
    return 0


if __name__ == "__main__":
    sys.exit(main())
