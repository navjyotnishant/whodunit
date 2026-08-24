#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Fails when a dashboard's contributor variable hides
# repositories with no recorded identity.
"""Check that the contributor filter cannot silently hide data.

The original variable query was:

    SELECT DISTINCT contributor FROM whodunit_repos WHERE contributor <> ''

which excludes repositories whose contributor was never recorded. Those
rows are then unreachable by any filter value AND absent from the
dropdown, so the data is synced and invisible. Measured before the fix,
115 of 131 sessions were in that state (NAV-110).

An empty contributor is a legitimate state — a repository instrumented
before metadata existed, or on a machine with no git identity configured —
so the answer is to make it selectable, not to assume it away.
"""

import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
BAD = "WHERE contributor <> ''"


def check_custom_variable(uid, var, problems):
    """Checks a custom variable, where the failure modes differ.

    A query variable's options come from the database. A custom one's are
    written into the dashboard, and Grafana parses that string in two ways
    that both fail by rendering a wrong chart rather than an error:

      - an option is `label : value`, in that order. Reversed, the dropdown
        shows the value while the SQL receives the label, so a comparison
        against a known value silently matches nothing.
      - options split on commas FIRST, so a label containing one becomes
        two options, neither of which is what was meant.

    Neither is visible in the rendered dashboard until someone notices a
    panel that should have data and does not.
    """
    name = var.get("name")
    query = var.get("query", "")
    if not query:
        problems.append(f"{uid}: custom variable {name} has no options")
        return

    # Counted before parsing: an option list is `label : value` pairs, so
    # the number of colons and the number of commas must agree. A label
    # containing a comma splits into two options and the second half looks
    # perfectly well-formed on its own - which is how a malformed list
    # passes a per-option check. Caught here only by counting.
    options = query.split(",")
    paired = [o for o in options if ":" in o]
    if paired and len(paired) != len(options):
        problems.append(
            f"{uid}: {name} has {len(options)} option(s) but only "
            f"{len(paired)} contain ':' - a label with a comma in it splits "
            f"into two options, and the stray half still parses as a pair")

    for option in options:
        if ":" not in option:
            # A bare option is legal Grafana and means label == value, so
            # it is only worth flagging when the others are paired: a
            # mixed list is far more likely a stray comma than intent.
            continue
        label, _, value = option.partition(":")
        label, value = label.strip(), value.strip()
        if not value:
            problems.append(
                f"{uid}: {name} option {option.strip()!r} has an empty value; "
                f"the dropdown would send nothing to the SQL")
        # A value that reads like prose is the reversed form: values are
        # tokens the SQL compares against, labels are for humans.
        if " " in value:
            problems.append(
                f"{uid}: {name} option {option.strip()!r} looks reversed - "
                f"Grafana reads 'label : value', so the SQL would receive "
                f"the human-readable half and match nothing")


def main():
    problems = []
    for path in sorted(glob.glob(os.path.join(HERE, "dashboards", "*.json"))):
        d = json.load(open(path))

        # Every SQL reference must be '${contributor:raw}', quoted here
        # and with :raw suppressing Grafana's own quoting.
        #
        # Grafana treats a selected value and the all-value differently,
        # which is what makes this worth a check rather than a comment:
        #
        #   a real contributor  is quoted by Grafana
        #   allValue            is substituted verbatim
        #
        # So no single spelling of a bare $contributor can serve both.
        # Written '$contributor', a real selection becomes ''email'' and
        # every filtered panel 400s. Written $contributor with quotes
        # moved into allValue, All works and real selections break — the
        # same failure with the cases swapped, which is exactly how this
        # was mis-diagnosed twice.
        #
        # ':raw' takes Grafana's quoting out of the picture entirely, so
        # the SQL means what it says for both cases. Verified against the
        # datasource rather than inferred: with :raw the executed query
        # reads = 'email' for a selection and = '__all__' for All.
        #
        # Checked statically because a dashboard that works on All is no
        # evidence — All was green through both broken states.
        for p in d.get("panels", []):
            for t in p.get("targets", []):
                sql = t.get("rawSql") or ""
                # The same rule for every filter variable, not only
                # contributor: the quoting failure is a property of how
                # Grafana substitutes an allValue against a real one, so
                # it applies identically to each.
                for name in ("contributor", "evidence", "team", "repo"):
                    if ("$" + name) in sql.replace("${%s:raw}" % name, ""):
                        problems.append(
                            f"{d['uid']}: {p.get('title') or '(untitled)'} uses a "
                            f"bare ${name}; it must be '${{{name}:raw}}' or "
                            f"Grafana's quoting breaks either All or every real "
                            f"selection")

        for v in d.get("templating", {}).get("list", []):
            if v.get("type") == "custom" and v.get("name") in ("evidence",):
                check_custom_variable(d["uid"], v, problems)
                if v.get("includeAll") and v.get("multi") is not False:
                    problems.append(
                        f"{d['uid']}: {v['name']} has includeAll without "
                        f"multi: false, so a selected value is substituted "
                        f"as {{value}} and matches nothing while All keeps "
                        f"working")
                continue
            if v.get("name") != "contributor":
                continue
            q = v.get("query", "")
            if BAD in q:
                problems.append(
                    f"{d['uid']}: contributor variable excludes empty values, so "
                    f"a repository with no recorded identity is unreachable")
            elif "COALESCE" not in q and "NULLIF" not in q:
                problems.append(
                    f"{d['uid']}: contributor variable does not offer an "
                    f"option for repositories with no recorded identity")
            elif v.get("includeAll") and v.get("multi") is not False:
                # With includeAll set and multi absent, Grafana treats the
                # variable as multi-capable and substitutes a chosen value
                # as {value}. That matches nothing, so every panel empties
                # the moment a contributor is selected — while "All" keeps
                # working, because __all__ is compared literally and never
                # braced. The filter looks functional and silently returns
                # no rows.
                problems.append(
                    f"{d['uid']}: contributor variable has includeAll without "
                    f"multi=false, so a selected value is substituted as "
                    f"{{value}} and matches nothing")
            elif q.upper().count("ORDER BY") > 1:
                # A malformed query fails silently: Grafana renders an
                # empty dropdown rather than an error, so the filter looks
                # present and offers nothing. This one was introduced by a
                # search-and-replace that appended ORDER BY to a query
                # that already had one.
                problems.append(
                    f"{d['uid']}: contributor variable has two ORDER BY "
                    f"clauses and is not valid SQL — the dropdown will be "
                    f"empty with no error shown")

    if problems:
        print("contributor filter would hide data:", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        return 1
    print("contributor filter reaches every repository")
    return 0


if __name__ == "__main__":
    sys.exit(main())
