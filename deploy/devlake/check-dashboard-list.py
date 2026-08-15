#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Fails when import-dashboards.sh does not import every
# dashboard in dashboards/.
"""Check that the importer's dashboard list matches what exists.

`import-dashboards.sh` names its dashboards in a literal list, because it
is curl-piped and has no checkout to glob. That makes the list the one
place a new dashboard must be registered by hand — and forgetting is
completely silent:

  * the dashboard is generated and committed
  * export-dashboards.py --check passes, because both copies agree
  * `go test ./...` passes, because none of this is Go
  * the importer runs, reports success, and imports one fewer than exists

Which is exactly what happened to whodunit-cost: written, exported,
committed, green build, absent from Grafana. Nothing in the pipeline
noticed, and the only way to find out was to ask Grafana what it had.

So the list is checked against the directory. A mismatch in either
direction is an error: a dashboard that exists and is not imported is
invisible, and one that is imported and does not exist makes the importer
report a failure on every run.
"""

import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "import-dashboards.sh")
DASHBOARDS = os.path.join(HERE, "dashboards")


def imported_names():
    """The dashboard names the importer loops over."""
    with open(SCRIPT) as f:
        body = f.read()

    # The loop spans lines with a trailing backslash, so the whole `for`
    # header is joined before the names are pulled out of it.
    body = body.replace("\\\n", " ")
    m = re.search(r"for name in ([^;]+); do", body)
    if not m:
        sys.exit(f"{SCRIPT}: could not find the dashboard loop — this checker "
                 f"is now looking for something that is not there, which is "
                 f"worse than not checking")
    return [n for n in m.group(1).split() if n]


def existing_names():
    return sorted(
        f[:-len(".json")]
        for f in os.listdir(DASHBOARDS)
        if f.endswith(".json")
    )


def main():
    listed = imported_names()
    existing = existing_names()

    missing = [n for n in existing if n not in listed]
    extra = [n for n in listed if n not in existing]

    if not missing and not extra:
        print(f"import-dashboards.sh imports all {len(existing)} dashboard(s)")
        return 0

    if missing:
        print("These dashboards exist but import-dashboards.sh does not "
              "import them, so they will never appear in Grafana:",
              file=sys.stderr)
        for n in missing:
            print(f"  {n}", file=sys.stderr)
    if extra:
        print("These are imported but do not exist, so the importer reports "
              "a failure on every run:", file=sys.stderr)
        for n in extra:
            print(f"  {n}", file=sys.stderr)

    print(f"\nfix: edit the `for name in ...` list in {SCRIPT}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
