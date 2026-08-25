#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-25
# Last updated: 2026-08-25
# Description: Fails when a dashboard declares a datasource variable, which
# cannot work under the import path these dashboards use.
"""Check that no dashboard offers a datasource dropdown that does nothing.

`import-dashboards.sh` binds these dashboards with Grafana's import-input
mechanism: it discovers the mysql datasource and passes its uid as
`DS_WHODUNIT`, which Grafana substitutes into every panel at import time and
bakes in permanently. A `$datasource` template variable cannot override
that. The dropdown renders, the selection changes, and every panel keeps
querying the datasource pinned at import.

That is worse than a missing control. Anyone pointing these dashboards at a
second database — a staging lake, a customer instance, a masked copy for
screenshots — selects it, sees the dropdown change, and reads the wrong
data with nothing indicating anything is wrong (WHO-129).

Reproduced 2026-08-25 against a second datasource: the dropdown read
`who129-probe`, pointing at an 11-commit fixture, while every panel showed
the 1,075-commit lake. The request body settles it — the browser posts the
uid pinned at import, never the variable's value.

Two fixes were tried first and neither worked, which is why this checks for
the variable's absence rather than for correct binding:

  * binding every panel and target to `${datasource}` — the import input
    overwrites the binding
  * clearing the variable's stale `current` value — the value was never
    consulted

Switching database is a re-import, which is the operation that actually
changes it:

    ./import-dashboards.sh --datasource <uid>
"""

import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def main():
    problems = []
    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))
        for v in d.get("templating", {}).get("list", []):
            if v.get("type") == "datasource":
                problems.append(
                    f"{d['uid']}: declares the datasource variable "
                    f"'{v.get('name')}', which renders a dropdown that cannot "
                    f"change which database any panel queries — the uid is "
                    f"pinned at import time. Remove it; switching database is "
                    f"./import-dashboards.sh --datasource <uid>")

    if problems:
        print("a dashboard offers a datasource dropdown that does nothing:",
              file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print("no dashboard offers a dead datasource dropdown")
    return 0


if __name__ == "__main__":
    sys.exit(main())
