#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Fails when a dashboard panel carries no description.
"""Check that every panel explains itself.

A panel is a claim, and a number whose derivation is invisible gets
believed or dismissed for the wrong reasons. Every panel should say what
it is, how it was calculated, and how to read it against its neighbours
(NAV-112).

This checks the first of those three, because it is the only one a script
can check. Length is a proxy for the rest and a weak one — a long
description can still fail to state the arithmetic — so the threshold is
set where it catches "no attempt was made" rather than where it grades
prose.

Row headers and text panels are exempt: a row is a label, and a text panel
IS its own explanation.
"""

import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Short enough that anything below it is a title restated, not an
# explanation. Deliberately not raised further: a check that fails on
# panels someone has genuinely documented gets suppressed rather than
# satisfied.
MIN_CHARS = 60

EXEMPT_TYPES = {"row", "text"}


def main():
    missing, thin = [], []

    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))
        uid = d.get("uid", os.path.basename(path))
        for p in d.get("panels", []):
            if p.get("type") in EXEMPT_TYPES:
                continue
            title = p.get("title") or "(untitled)"
            desc = (p.get("description") or "").strip()
            if not desc:
                missing.append(f"{uid}: {title}")
            elif len(desc) < MIN_CHARS:
                thin.append(f"{uid}: {title} ({len(desc)} chars)")

    if not missing and not thin:
        print("every panel carries a description")
        return 0

    if missing:
        print("panels with no description at all:", file=sys.stderr)
        for m in missing:
            print(f"  {m}", file=sys.stderr)
    if thin:
        print(f"\npanels whose description is under {MIN_CHARS} characters "
              f"— a title restated is not an explanation:", file=sys.stderr)
        for t in thin:
            print(f"  {t}", file=sys.stderr)

    print("\nEach should say what it is, how it is calculated, and how to "
          "read it\nagainst the panels around it. Where a plausible-but-wrong "
          "formula\nexists, state the real one.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
