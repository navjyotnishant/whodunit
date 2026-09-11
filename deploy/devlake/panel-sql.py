#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-13
# Last updated: 2026-08-13
# Description: Extract runnable SQL from a Grafana dashboard, for verify.sh.

"""Read a Grafana dashboard API response, print one runnable query per line.

Used by verify.sh to answer the only question that matters after an import:
do the panels actually return rows. A dashboard whose datasource is bound and
whose SQL is valid can still draw nothing — that is what a pinned team id or
a wrong timezone does — and it looks exactly like success from the import
side.

Substitution is deliberately approximate. The goal is a query the database
will run, not a faithful reproduction of a dashboard's state: $__timeFilter
becomes a wide window, variables take their declared default or a wildcard.
A panel that returns rows under those conditions has working SQL against
real data, which is what is being checked.

    curl .../api/dashboards/uid/whodunit | python3 panel-sql.py
"""

import json
import re
import sys

# Grafana macros. The time filter is widened rather than honored: a panel
# scoped to "last 6 hours" would return nothing on a database whose data is a
# day old, and that is not the failure being looked for.
# A leftover Grafana variable, as distinct from a `$` that is regex syntax.
LEFTOVER_VARIABLE = re.compile(r"\$\{?[A-Za-z_]\w*")

MACROS = {
    r"\$__timeFilter\([^)]*\)": "1=1",
    r"\$__timeGroup\(([^,]+),[^)]*\)": r"\1",
    r"\$__timeGroupAlias\(([^,]+),[^)]*\)": r"\1",
    r"\$__timeFrom\(\)": "'1970-01-01'",
    r"\$__timeTo\(\)": "'2099-01-01'",
    r"\$__unixEpochFilter\([^)]*\)": "1=1",
    # Epoch bounds, widened for the same reason as the time filter. These are
    # what whodunit's own panels use, since the journal stores nanoseconds.
    r"\$__unixEpochFrom\(\)": "0",
    r"\$__unixEpochTo\(\)": "4102444800",
    r"\$__unixEpochGroup\(([^,]+),[^)]*\)": r"\1",
    r"\$__interval_ms": "86400000",
    r"\$__interval": "1d",
    r"\$__rate_interval": "1d",
}


def variable_defaults(dashboard: dict) -> dict:
    """Map each template variable to a value that will not exclude rows.

    A multi-value variable resolves to the SQL wildcard rather than a
    concrete pick, so `WHERE x LIKE '$var'` matches everything instead of
    matching whatever this instance happened to have first.
    """
    out = {}
    for var in dashboard.get("templating", {}).get("list", []):
        name = var.get("name")
        if not name:
            continue

        current = var.get("current") or {}
        value = current.get("value")
        if isinstance(value, list):
            value = value[0] if value else None

        kind = var.get("type")
        if kind in ("custom", "textbox", "constant", "interval") and value:
            out[name] = str(value)
        elif kind == "query":
            # Unpinned by export-dashboards.py, and that is the point: a
            # query variable resolves against the importing instance. '%'
            # keeps a LIKE clause from filtering everything out.
            out[name] = "%"
        elif value not in (None, "", "$__all"):
            out[name] = str(value)
        else:
            out[name] = "%"
    return out


def _substitute(variables: dict, match, name: str | None = None) -> str:
    """Replace one variable reference, quoting it only if it needs it.

    A bare `%` works in `LIKE $agent` and is a syntax error in `IN ($agent)`,
    so a lone variable is quoted. But `'$tz'` is already inside quotes in the
    SQL, and quoting again gives `''+00:00''` — equally broken. The match
    tells the two apart: it captures the surrounding quotes when they exist.
    """
    text = match.group(0)
    if name is None:
        name = next(g for g in match.groups() if g)
    value = variables.get(name, "%")

    if text.startswith("'"):
        # The dashboard supplies the quotes.
        return "'" + value.replace("'", "''") + "'"
    if re.fullmatch(r"-?\d+(\.\d+)?", value):
        return value
    return "'" + value.replace("'", "''") + "'"


def panels(dashboard: dict):
    """Yield every panel, including those nested inside collapsed rows."""
    for panel in dashboard.get("panels", []):
        yield panel
        for child in panel.get("panels", []):
            yield child


def runnable(sql: str, variables: dict) -> str:
    for pattern, replacement in MACROS.items():
        sql = re.sub(pattern, replacement, sql)

    # Formatters first (${var:sqlstring}, ${var:csv}), before the bare-name
    # pass below can chew the "${var" prefix off and strand the ":csv}".
    sql = re.sub(
        r"'\$\{(\w+):\w+\}'|\$\{(\w+):\w+\}",
        lambda m: _substitute(variables, m),
        sql,
    )

    # A variable the dashboard already wrapped in quotes ('$tz') takes its
    # bare value; one standing alone ($agent) is quoted here. Handling both
    # in one pattern, because quoting an already-quoted variable produces
    # ''+00:00'' and quoting nothing produces IN (%) — both syntax errors,
    # and each is what you get from fixing only the other.
    for name in variables:
        sql = re.sub(
            r"'\$\{?%s\}?'|\$\{?%s\}?(?!\w)" % (re.escape(name), re.escape(name)),
            lambda m, n=name: _substitute(variables, m, n),
            sql,
        )

    # Strip `--` line comments BEFORE flattening. Collapsing newlines turns a
    # trailing comment into one that swallows the entire rest of the query,
    # and MySQL then reports `syntax error near ''` — an unexpected end of
    # input, pointing nowhere near the actual line. The Burn-up panel carries
    # two such comments; it works in Grafana, which sends the SQL unflattened,
    # and only breaks here (WHO-242).
    sql = strip_line_comments(sql)

    # One line per query, since verify.sh reads them line by line.
    return " ".join(sql.split())


def strip_line_comments(sql: str) -> str:
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


def main() -> int:
    payload = json.load(sys.stdin)
    dashboard = payload.get("dashboard", payload)
    variables = variable_defaults(dashboard)

    seen = set()
    for panel in panels(dashboard):
        for target in panel.get("targets", []):
            sql = target.get("rawSql")
            if not sql or target.get("hide"):
                continue
            query = runnable(sql, variables)
            if not query or query in seen:
                continue
            # Anything still carrying an unresolved variable would fail as a
            # sql error and be reported as a broken panel, which would be a
            # lie about the dashboard. Skipped — but said out loud on stderr,
            # because silently dropping queries makes a partial check look
            # like a complete one.
            # A variable is `$` followed by a word character; a regex
            # end-anchor is `$` followed by a quote or end of string. Testing
            # `"$" in query` cannot tell them apart, so every panel carrying
            # the issue_key guard `'^[A-Z][A-Z0-9]+-[0-9]+$'` was skipped —
            # 33 of them, i.e. exactly the queries written most carefully,
            # and verify.sh reported success having checked none (WHO-242).
            # The tell was the message itself: findall matched nothing, so it
            # printed "unresolved" with an empty list.
            unresolved = set(LEFTOVER_VARIABLE.findall(query))
            if unresolved:
                print(
                    f"  (skipped a panel query: unresolved {', '.join(sorted(unresolved))})",
                    file=sys.stderr,
                )
                continue
            seen.add(query)
            print(query)
    return 0


if __name__ == "__main__":
    sys.exit(main())
