#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-18
# Last updated: 2026-09-18
# Description: Panel-walking and SQL-cleaning shared by the dashboard scripts.

"""What `panel-sql.py` and `build-catalog.py` both need to read a dashboard.

Both scripts walk the same dashboard JSON and clean the same SQL out of it,
and both grew their own copy of that code. The copies were identical apart
from type hints — which is the kind of duplication that stays identical
right up until someone fixes a bug in one of them.

Imported rather than copied, so a fix to the comment stripper reaches the
catalog and the smoke check at once.
"""


def panels(dashboard):
    """Yield every panel, including those nested inside collapsed rows.

    A collapsed row holds its children in its own `panels` key, so a flat
    read of the top-level list misses them. The `or []` matters: a collapsed
    row can carry `"panels": null` rather than an empty list.
    """
    for panel in dashboard.get("panels", []):
        yield panel
        for child in panel.get("panels", []) or []:
            yield child


def strip_line_comments(sql):
    """Remove `-- ...` comments, respecting single-quoted string literals.

    Quote-aware because `'--'` inside a literal is data, not a comment, and
    stripping from there would truncate a valid query. MySQL also requires
    whitespace after `--` for it to open a comment, which is what leaves the
    `-[0-9]+` in the issue_key guard's regex alone.
    """
    out, in_string, i = [], False, 0
    while i < len(sql):
        ch = sql[i]
        if in_string:
            out.append(ch)
            if ch == "\\" and i + 1 < len(sql):
                out.append(sql[i + 1])
                i += 2
                continue
            if ch == "'":
                # '' inside a literal is an escaped quote, not the end.
                if i + 1 < len(sql) and sql[i + 1] == "'":
                    out.append("'")
                    i += 2
                    continue
                in_string = False
            i += 1
            continue
        if ch == "'":
            in_string = True
            out.append(ch)
            i += 1
            continue
        if sql[i : i + 3] in ("-- ", "--\t", "--\n") or sql[i:] == "--":
            j = sql.find("\n", i)
            if j == -1:
                break
            out.append("\n")
            i = j + 1
            continue
        out.append(ch)
        i += 1
    return "".join(out)
