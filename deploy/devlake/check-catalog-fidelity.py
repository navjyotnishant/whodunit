#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-10
# Last updated: 2026-09-10
# Description: WHO-220 — proves catalog.json's SQL is the SAME SQL the
# dashboards run, not a plausible rewrite of it.

"""Bind every catalog entry to the same Grafana defaults panel-sql.py uses,
and diff the two. Pure text comparison — no database needed, so this is
cheap enough to run in CI alongside the other guard scripts.

Exactly one difference is expected and tolerated: `$__timeFilter(col)`.
panel-sql.py collapses it to `1=1` on purpose — it is a smoke test, and a
panel scoped to "last 6 hours" would return nothing against day-old data.
The catalog keeps it as a real bound comparison, because run_metric has to
honour a caller's window. Anything else that differs is a real defect.

Quoting is taken from the CATALOG SQL, never re-derived from whether a
default value "looks numeric" — `CAST('12' AS DECIMAL)` and
`CAST(12 AS DECIMAL)` are the same query and different text, and the
dashboard author's own choice of `'$seats'` vs `$seats` is what decided
which one this is. Guessing from the value produced false mismatches on
every Investment Case metric during development of this check.
"""

import glob
import importlib.util
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).parent

spec = importlib.util.spec_from_file_location("panel_sql", HERE / "panel-sql.py")
panel_sql = importlib.util.module_from_spec(spec)
spec.loader.exec_module(panel_sql)

spec2 = importlib.util.spec_from_file_location("build_catalog", HERE / "build-catalog.py")
build_catalog = importlib.util.module_from_spec(spec2)
spec2.loader.exec_module(build_catalog)

TIME_FILTER_WIDENED = re.compile(r"(\S+) BETWEEN '1970-01-01' AND '2099-01-01'")


def bind(entry: dict, variables: dict) -> str:
    """Bind an entry's placeholders to panel-sql.py's resolved defaults."""
    parts = entry["sql"].split("?")
    out = []
    for i, part in enumerate(parts[:-1]):
        name = entry["params"][i]
        literal = {
            "time_from": "0",
            "time_to": "4102444800",
            "time_from_dt": "'1970-01-01'",
            "time_to_dt": "'2099-01-01'",
        }.get(name)
        if literal is None:
            value = variables.get(name, "%")
            # A `'` right before this `?` means the source SQL already
            # quoted it; match that rather than guessing from the value.
            literal = value if part.endswith("'") else "'" + value.replace("'", "''") + "'"
        out.append(part)
        out.append(literal)
    out.append(parts[-1])
    return " ".join("".join(out).split())


def main() -> int:
    catalog = {m["id"]: m for m in json.load(open(HERE / "catalog.json"))["metrics"]}
    identical = time_filter_only = mismatches = 0
    bad = []

    for path in sorted(glob.glob(str(HERE / "dashboards" / "*.json"))):
        dashboard = json.load(open(path))
        title = dashboard.get("title")
        variables = panel_sql.variable_defaults(dashboard)

        for panel in panel_sql.panels(dashboard):
            if panel.get("type") in build_catalog.EXEMPT_TYPES:
                continue
            for target in panel.get("targets") or []:
                sql = target.get("rawSql")
                if not sql or target.get("hide"):
                    continue
                metric_id = build_catalog.slug(title, panel.get("title") or "")
                if metric_id not in catalog:
                    break  # refused panel (e.g. literal '?'); nothing to compare
                bound = bind(catalog[metric_id], variables)
                reference = " ".join(panel_sql.runnable(sql, variables).split())
                if bound == reference:
                    identical += 1
                elif TIME_FILTER_WIDENED.sub("1=1", bound) == reference:
                    time_filter_only += 1
                else:
                    mismatches += 1
                    bad.append(metric_id)
                break

    total = identical + time_filter_only + mismatches
    print(
        f"catalog fidelity: {identical} identical, {time_filter_only} "
        f"differ only by $__timeFilter widening, {mismatches} mismatched "
        f"(of {total} compared)"
    )
    if mismatches:
        print("\nmismatched entries:", file=sys.stderr)
        for metric_id in bad:
            print(f"  {metric_id}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
