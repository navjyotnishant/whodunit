#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-10
# Last updated: 2026-09-10
# Description: Extracts the dashboards' panel queries into a catalog the MCP
# server publishes, with placeholders instead of Grafana variables.

"""Generate catalog.json from the mounted dashboards.

# Why this exists

The dashboards are the only validated corpus of questions this database can
answer. Each panel carries SQL that has been run against a real instance, a
description written as a caveat, and a declared chart type. An MCP server
that publishes *those* is a publisher of reviewed queries; one that writes
its own SQL is a machine for turning quiet wrong numbers into confident
English.

The repo has already paid to learn the difference. `issue_key` text-matched
against commit messages reported 11 AI-assisted issues where there were 0
(NAV-122), hand-written by the schema's own author. `check-issue-key-guard.py`
and `check-status-set.py` exist to catch that class. A catalog inherits every
one of those guards, because it inherits the queries they check.

# The parameter substitution, and why it is not panel-sql.py

`panel-sql.py` produces a query a database will *run*, for a smoke check:
variables collapse to their defaults or to `%`. That is the wrong target
here. A catalog entry has to stay parameterized, so `run_metric` can bind a
team or a contributor at call time.

So each Grafana variable becomes a `?` placeholder and is recorded in
`params`, in the order MySQL will bind them. The two macros that bound time
become placeholders too. `$__timeFilter(col)` is the exception: it expands to
a BETWEEN over the same two bounds, because it is a macro over a column
rather than a bare value.

# What is deliberately excluded

Text and row panels. A markdown header is not a metric, and including them
is what produces "duplicate ids" — 13 pairs of untitled text panels, every
collision in the corpus. `check-panel-descriptions.py` already exempts
exactly these two types, for the same reason.

Panels whose SQL still carries an unresolved `$` after substitution. Such an
entry would fail at bind time and be reported as an empty metric, which is a
lie about the dashboard rather than an error. They are counted and named on
stderr — silently dropping queries makes a partial catalog look complete.

    ./build-catalog.py            # write catalog.json
    ./build-catalog.py --check    # verify it is current (for CI)
"""

import argparse
import hashlib
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).parent
SOURCE = HERE / "dashboards"
OUTPUT = HERE / "catalog.json"

# A markdown header is not a metric. Same exemption, same reason, as
# check-panel-descriptions.py.
EXEMPT_TYPES = {"row", "text"}

# The two epoch macros are bare values and become placeholders directly.
# $__timeFilter is a macro over a COLUMN, so it expands to the comparison it
# stands for rather than to a single '?'.
EPOCH_FROM = re.compile(r"\$__unixEpochFrom\(\)")
EPOCH_TO = re.compile(r"\$__unixEpochTo\(\)")
TIME_FILTER = re.compile(r"\$__timeFilter\(([^)]+)\)")

# A Grafana variable, with or without braces and with or without a formatter,
# and capturing the quotes when the dashboard supplied them. The quotes
# matter: '${tz}' already sits inside them, and leaving them behind produces
# '?' — a literal question mark, not a placeholder. That is the exact shape
# of the ''+00:00'' bug panel-sql.py exists to work around, and it is silent.
VARIABLE = re.compile(r"'\$\{(\w+)(?::\w+)?\}'|'\$(\w+)'|\$\{(\w+)(?::\w+)?\}|\$(\w+)")

# WHO-254's min_n: a query's own HAVING COUNT(...) >= N (or a named alias
# like `n`) is a threshold the query author already decided on and already
# enforces — extracting it is reading a fact out of the SQL, not guessing
# one. Deliberately narrow to this exact HAVING shape rather than trying to
# recognize every way a query could withhold small groups; a query that
# enforces a minimum some other way (a WHERE on a precomputed count, say)
# is not detected, and that entry simply carries no min_n rather than a
# wrong one.
MIN_N = re.compile(r"HAVING\s+(?:COUNT\((?:DISTINCT\s+)?[^)]*\)|n)\s*>=\s*(\d+)", re.I)

# A leftover Grafana variable, as distinct from a `$` that is regex syntax.
#
# This distinction is the whole check. `'^[A-Z][A-Z0-9]+-[0-9]+$'` is the
# issue_key guard: `$` there is the end-anchor, and 33 panels carry it —
# every one of the queries that protects against NAV-122, where issue_key
# text-matched against commit messages reported 11 AI-assisted issues out of
# 0. A bare `"$" in query` test drops exactly those panels, so the catalog
# would silently omit the most carefully written queries in the corpus while
# reporting itself complete. A variable is `$` followed by a word character;
# an anchor is `$` followed by a quote or end of string.
LEFTOVER_VARIABLE = re.compile(r"\$\{?[A-Za-z_]\w*")

# Macros and variables in one alternation, so a single pass sees them in the
# order they appear. Macros lead: `$__unixEpochFrom` would otherwise match
# the bare-variable branch as the variable named `__unixEpochFrom`.
COMBINED = re.compile(
    r"\$__unixEpochFrom\(\)|\$__unixEpochTo\(\)|\$__timeFilter\([^)]+\)|"
    r"'\$\{(\w+)(?::\w+)?\}'|'\$(\w+)'|\$\{(\w+)(?::\w+)?\}|\$(\w+)"
)

# The two epoch-macro names never appear in any dashboard's templating.list —
# they come from $__unixEpochFrom()/$__unixEpochTo(), not a Grafana variable —
# so their schema has no source to read and is declared here once. time_from
# and time_from_dt bound the same window in two literal shapes (WHO-248's
# "requires_with" case): a NULL/absent time_from_dt lets $__timeFilter compare
# a DATETIME column to two epoch integers, which MySQL accepts and silently
# returns nothing, which is exactly the "day"/grain failure this schema exists
# to prevent one level up.
TIME_PARAM_SCHEMA = {
    "time_from": {
        "type": "string",
        "required": True,
        "format": "iso-date",
        "example": "2026-08-11",
        "description": "Start of the reporting window (epoch macro).",
    },
    "time_to": {
        "type": "string",
        "required": True,
        "format": "iso-date",
        "example": "2026-09-11",
        "description": "End of the reporting window (epoch macro).",
    },
    "time_from_dt": {
        "type": "string",
        "required": True,
        "format": "iso-date",
        "example": "2026-08-11",
        "requires_with": ["time_to_dt"],
        "description": (
            "Start of the reporting window, as a DATETIME literal — required "
            "alongside time_from_dt whenever a query also binds time_from/"
            "time_to for the same window; see time_from."
        ),
    },
    "time_to_dt": {
        "type": "string",
        "required": True,
        "format": "iso-date",
        "example": "2026-09-11",
        "requires_with": ["time_from_dt"],
        "description": "End of the reporting window, as a DATETIME literal.",
    },
}


def custom_allowed(query):
    """Parse a `type: "custom"` variable's `label : value,label : value` query
    into its allowed values (the value half of each pair).

    Grafana's own custom-variable UI shows the label; the SQL receives the
    value. A pair with no ` : ` has no separate label — the token itself is
    the value.
    """
    values = []
    for token in (query or "").split(","):
        token = token.strip()
        if not token:
            continue
        _, _, value = token.rpartition(" : ")
        values.append((value or token).strip())
    return values


def variable_param_schema(variable):
    """Build one WHO-248 parameter-schema entry from a Grafana templating
    variable definition (see dashboards' `templating.list[]`).

    `type: "custom"` variables enumerate their own domain (grain, tz,
    evidence) — that becomes `allowed`. `type: "textbox"` variables carry a
    literal default (seats, hourly_rate) rather than a domain. `type: "query"`
    variables (contributor, team, repo, board, agent) are populated from a
    live SQL query and have no static domain here; WHO-250's list_dimensions
    is the source for those, not this generator — a dashboard's own query
    result can change between builds, which is exactly the kind of thing this
    script does not re-run against a live database.

    An `allValue` of `None` (Grafana's un-set default, distinct from the
    string "null") means the variable has no wildcard — `board` is exactly
    this case, and passing "__all__" for it is a validation error, not a
    style choice.
    """
    name = variable.get("name")
    var_type = variable.get("type")
    schema = {"name": name, "type": "string", "required": True}

    all_value = variable.get("allValue")
    if all_value:
        schema["required"] = False
        schema["default"] = all_value
        schema["description"] = (
            f"Filter by {name}; {all_value!r} (the default) matches everything."
        )

    if var_type == "custom":
        allowed = custom_allowed(variable.get("query"))
        if allowed:
            schema["allowed"] = sorted(set(allowed) | ({all_value} if all_value else set()))
        if name == "grain":
            schema["format"] = "mysql-date-format"
            schema["description"] = (
                "Bucket size for the time series, as a MySQL DATE_FORMAT "
                "mask — not a word like \"day\"."
            )
            schema["example"] = "%Y-%m-%d"
    elif var_type == "textbox":
        schema["type"] = "number"
        schema["required"] = False
        default = variable.get("query")
        if default is not None:
            schema["default"] = default
            schema["example"] = default
    elif var_type == "query":
        schema["format"] = "opaque-id"
        if all_value is None:
            schema.setdefault(
                "description",
                f"{name} has no catch-all value — omitting it is not the "
                "same as matching everything.",
            )

    return schema


def dashboard_param_schemas(dashboard):
    """Map every templating variable in one dashboard to its param schema."""
    return {
        v["name"]: variable_param_schema(v)
        for v in dashboard.get("templating", {}).get("list", [])
        if v.get("name")
    }


def panels(dashboard):
    """Yield every panel, including those nested inside collapsed rows."""
    for panel in dashboard.get("panels", []):
        yield panel
        for child in panel.get("panels", []) or []:
            yield child


def parameterize(sql):
    """Return (sql_with_placeholders, ordered param names).

    Order is what makes this correct: MySQL binds positionally, so the list
    must be built in the order the placeholders appear in the final string,
    not in the order the names were discovered. Both passes therefore append
    as they substitute, left to right.
    """
    params = []

    # ONE left-to-right pass over macros and variables together.
    #
    # Two sequential passes look equivalent and are not: re.sub walks the
    # string each time, so every macro param is appended before any variable
    # param, while their placeholders INTERLEAVE in the SQL. The list then
    # describes a different order than the string, and the driver — which
    # binds strictly by position — puts a board id where a date belongs.
    # Sometimes that raises (`Incorrect DATETIME value`); on 248 of these
    # queries it would simply return the wrong rows. Measured here: 31
    # queries misbound this way before the passes were merged.
    def replace(match):
        text = match.group(0)
        if text.startswith("$__unixEpochFrom"):
            params.append("time_from")
            return "?"
        if text.startswith("$__unixEpochTo"):
            params.append("time_to")
            return "?"
        if text.startswith("$__timeFilter"):
            column = TIME_FILTER.match(text).group(1).strip()
            # Named apart from time_from/time_to deliberately. The epoch
            # macros bound an integer column of nanoseconds; $__timeFilter
            # bounds a DATETIME. Same window, different literal — and an
            # epoch integer in a DATETIME column raises, while a date string
            # in the epoch comparison silently returns nothing.
            params.extend(["time_from_dt", "time_to_dt"])
            return f"{column} BETWEEN ? AND ?"
        name = next(g for g in match.groups() if g)
        params.append(name)
        return "?"

    sql = COMBINED.sub(replace, sql)

    # Strip `--` line comments BEFORE flattening. Collapsing newlines turns a
    # trailing comment into one that swallows the entire rest of the query,
    # and MySQL then reports `syntax error near ''` — an unexpected end of
    # input, pointing nowhere near the actual line. The Burn-up panel carries
    # two such comments and is the reason this exists.
    #
    # Quote-aware, because `'--'` inside a string literal is data, not a
    # comment, and stripping from there would truncate a valid query. MySQL
    # also requires whitespace after `--`, which is what keeps this from
    # eating the `-[0-9]+` in the issue_key guard's regex.
    sql = strip_line_comments(sql)
    return " ".join(sql.split()), params


def strip_line_comments(sql):
    """Remove `-- ...` comments, respecting single-quoted string literals."""
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
        # MySQL needs whitespace after `--` for it to be a comment, which is
        # what leaves `-[0-9]+$` in the issue_key regex alone.
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


def param_schema_list(names, dashboard_schemas):
    """Turn a positional (possibly duplicated) param name list into WHO-248's
    typed, deduplicated schema — order-preserving on first occurrence.

    `params` stays positional for SQL binding (see parameterize()); this is a
    second, human/agent-facing view derived from it, never the reverse — the
    generated SQL is still the source of truth for what actually gets bound.
    """
    seen = set()
    out = []
    for name in names:
        if name in seen:
            continue
        seen.add(name)
        if name in TIME_PARAM_SCHEMA:
            out.append({"name": name, **TIME_PARAM_SCHEMA[name]})
        elif name in dashboard_schemas:
            out.append(dashboard_schemas[name])
        else:
            # A param name that is neither a time macro nor a declared
            # dashboard variable. Emitting an untyped placeholder rather than
            # dropping it keeps every SQL placeholder accounted for in
            # params_schema, at the cost of that one entry being no more
            # informative than the old flat list — better than silently
            # losing a bindable name.
            out.append({"name": name, "type": "string", "required": True})
    return out


def entries():
    """Yield one catalog entry per query panel, and a list of skips."""
    out, skipped, literal_q = [], [], []

    for path in sorted(SOURCE.glob("*.json")):
        dashboard = json.loads(path.read_text())
        title = dashboard.get("title") or path.stem
        dashboard_schemas = dashboard_param_schemas(dashboard)

        for panel in panels(dashboard):
            if panel.get("type") in EXEMPT_TYPES:
                continue
            for target in panel.get("targets") or []:
                sql = target.get("rawSql")
                if not sql or target.get("hide"):
                    continue

                query, params = parameterize(sql)

                # A '?' the SQL brought with it, rather than one we emitted.
                # `REGEXP_REPLACE(e.tool, '^mcp__(plugin_[a-z]+_)?', '')` has
                # one as a regex quantifier, and the driver cannot tell it
                # from a placeholder: it binds positionally, so every
                # parameter after it lands one slot off and the query still
                # runs. A wrong number that raises no error is the failure
                # this whole catalog exists to prevent, so such a panel is
                # refused rather than published.
                if query.count("?") != len(params):
                    literal_q.append(f"{title} / {panel.get('title')}")
                    continue

                unresolved = sorted(set(LEFTOVER_VARIABLE.findall(query)))
                if unresolved:
                    skipped.append(
                        f"{title} / {panel.get('title')}: {', '.join(unresolved)}"
                    )
                    continue

                entry = {
                    "id": slug(title, panel.get("title") or ""),
                    "dashboard": title,
                    "title": panel.get("title") or "",
                    "description": (panel.get("description") or "").strip(),
                    "chart_type": panel.get("type"),
                    # Positional, duplicated, order-significant — what
                    # the driver binds against. Kept as-is (WHO-248):
                    # existing consumers read this as a flat name list,
                    # and params_schema is additive, not a replacement.
                    "params": params,
                    # Typed, deduplicated, human/agent-facing (WHO-248).
                    "params_schema": param_schema_list(params, dashboard_schemas),
                    "sql": query,
                }
                min_n_match = MIN_N.search(query)
                if min_n_match:
                    # WHO-254: the query's own enforced minimum group size —
                    # present only when the SQL actually declares one, so
                    # its absence here means "not detected", never "zero".
                    entry["min_n"] = int(min_n_match.group(1))
                out.append(entry)
                break  # one query per panel; the rest are overlays

    return out, skipped, literal_q


def slug(dashboard, title):
    """A stable id from (dashboard, title).

    Verified unique across the corpus once text panels are excluded: every
    collision in the 310 panels is a pair of untitled text panels, which are
    not metrics and never reach here.
    """
    text = f"{dashboard} {title}".lower()
    return re.sub(r"-+", "-", re.sub(r"[^a-z0-9]+", "-", text)).strip("-")


def dimensions():
    """Collect every `type: "query"` templating variable across all
    dashboards into one deduplicated map, for WHO-250's list_dimensions.

    A query-type variable (board, contributor, team, repo, agent) has no
    static domain — its values come from running its own SQL against the
    lake, which is exactly what run_metric already does for a catalog
    entry's SQL, and exactly what this generator does NOT do (it never
    connects to a database; see the module docstring). So this function
    does not resolve values — it collects the DISCOVERY QUERY itself, which
    whodunit-mcp's list_dimensions tool runs live at call time.

    A name that appears with more than one distinct query across dashboards
    (only `board` today: an unfiltered variant and a Linear/Jira-only
    variant used by the exec dashboards) resolves to the broadest one — a
    caller discovering values should see every board that exists, not only
    the subset one dashboard happened to filter to. run_metric's own SQL
    still applies whatever filter that metric's query declares regardless
    of what list_dimensions returned.
    """
    by_name = {}
    for path in sorted(SOURCE.glob("*.json")):
        dashboard = json.loads(path.read_text())
        for v in dashboard.get("templating", {}).get("list", []):
            if v.get("type") != "query" or not v.get("name"):
                continue
            name, query = v["name"], v.get("query") or ""
            existing = by_name.get(name)
            # Shorter wins: a WHERE clause narrows the result set, so the
            # filtered variant is the LONGER query text, not the broader
            # one. board's exec-dashboard variant adds a WHERE; its
            # unfiltered form (no filter at all) is what a caller
            # discovering values should see.
            if existing is None or len(query) < len(existing["query"]):
                by_name[name] = {"query": query, "label": v.get("label") or name}

    out = {}
    for name, info in sorted(by_name.items()):
        out[name] = {
            # __text/__value are Grafana's own required aliases for a
            # query-variable's label/value columns (see the templating
            # queries themselves) — list_dimensions runs this SQL as-is and
            # reads those two column names back.
            "label_column": "__text",
            "value_column": "__value",
            "query": " ".join(info["query"].split()),
        }
    return out


def render():
    catalog, skipped, literal_q = entries()

    ids = [e["id"] for e in catalog]
    duplicates = sorted({i for i in ids if ids.count(i) > 1})
    if duplicates:
        # Fail rather than emit a catalog where run_metric(id) is ambiguous.
        # Which of two same-named panels answered is not a question the
        # caller can be left to resolve.
        print(
            "duplicate metric ids:\n  " + "\n  ".join(duplicates),
            file=sys.stderr,
        )
        raise SystemExit(1)

    # The hash covers the SOURCE dashboards, not this catalog: it is what a
    # downstream consumer pins to, so it must change when the dashboards
    # change even if the extraction happens to produce the same rows.
    digest = hashlib.sha256()
    for path in sorted(SOURCE.glob("*.json")):
        digest.update(path.read_bytes())

    payload = {
        "source_sha256": digest.hexdigest(),
        "dashboard_count": len(list(SOURCE.glob("*.json"))),
        "metric_count": len(catalog),
        "metrics": catalog,
        "dimensions": dimensions(),
    }
    return json.dumps(payload, indent=2, ensure_ascii=False) + "\n", skipped, literal_q


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if catalog.json is out of date, instead of writing it",
    )
    args = parser.parse_args()

    want, skipped, literal_q = render()

    for line in skipped:
        print(f"  (skipped a panel: unresolved {line})", file=sys.stderr)
    for line in literal_q:
        print(f"  (skipped a panel: literal '?' in its SQL: {line})", file=sys.stderr)

    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != want:
            print(
                f"{OUTPUT.name} is out of date\n\nregenerate it with:  ./build-catalog.py",
                file=sys.stderr,
            )
            return 1
        print(f"{json.loads(want)['metric_count']} catalog metrics are current")
        return 0

    OUTPUT.write_text(want)
    print(f"wrote {OUTPUT.name}: {json.loads(want)['metric_count']} metrics")
    return 0


if __name__ == "__main__":
    sys.exit(main())
