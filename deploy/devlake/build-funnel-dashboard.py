#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Generates whodunit-funnel.json — the six-stage AI
# productivity funnel.
"""Build the AI Productivity Funnel.

Converted from hand-maintained JSON to a generator for the same reason
build-cost-dashboard.py is one: the styling repeats across every panel,
and changing it in ten places by hand is how a dashboard ends up looking
like three different dashboards.

## The framing this dashboard exists to protect

**The stages do not multiply.** 70% adoption x 80% engagement x 60%
assisted work is not a productivity figure — it is arithmetic on unrelated
denominators. Each stage is measured on its own and reported on its own.

**Adoption is not productivity.** Stages 1 and 2 say the tool is being
used. That is a precondition, not a result, and the layout keeps them
visually separate from the value stages so the two cannot be mistaken for
each other.

**Stage 6 is deliberately unmeasurable.** Business value needs outcomes
this tool cannot see. Saying so is the point of the stage existing —
deleting it would imply the funnel ends at stage 5.

## What changed from the hand-written version

A layout bug: the "Value funnel" row header sat at y=24 while stages 3
and 4 rendered at y=7, above their own section. Grafana rows group by
vertical position, and the two-column layout defeated that. Sections are
now full-width bands.
"""

import json
import os

# NOT quoted. Grafana's MySQL datasource quotes an interpolated
# variable itself, so writing '$contributor' produces ''value'' — a
# syntax error that fails the whole query with a 400. The "All" case
# hid it: Grafana substitutes the all-value as a bare __all__ token
# rather than a quoted string, so only real selections broke.
CONTRIBUTOR = "($contributor = '__all__' OR r.contributor = $contributor)"

# Same treatment as whodunit-cost, so the two read as one product.
BARGAUGE = {"displayMode": "gradient", "orientation": "horizontal",
            "showUnfilled": True, "valueMode": "text",
            "text": {"valueSize": 18, "titleSize": 12},
            "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}
STAT = {"graphMode": "none", "textMode": "value",
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}}

CONTINUOUS_BLUES = {"mode": "continuous-BlPu"}
CONTINUOUS_GREENS = {"mode": "continuous-GrYlRd"}
NEUTRAL = {"mode": "fixed", "fixedColor": "text"}

SQL = json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                  "funnel_sql.json")))


def panel(pid, title, ptype, x, y, w, h, sql, *, unit=None, decimals=None,
          novalue=None, description=None, options=None, color=None,
          overrides=None):
    p = {"id": pid, "title": title, "type": ptype, "datasource": "${datasource}",
         "gridPos": {"x": x, "y": y, "w": w, "h": h},
         "targets": [{"refId": "A", "format": "table", "rawSql": sql}],
         "fieldConfig": {"defaults": {}, "overrides": overrides or []}}
    if description:
        p["description"] = description
    d = p["fieldConfig"]["defaults"]
    if unit:
        d["unit"] = unit
    if decimals is not None:
        d["decimals"] = decimals
    if novalue:
        d["noValue"] = novalue
    d["color"] = color or NEUTRAL
    # Grafana injects a default green/red threshold pair onto stats and
    # gauges when none is given, which overrides the palette. Cleared.
    d["thresholds"] = {"mode": "absolute", "steps": [{"color": "text", "value": None}]}
    if options:
        p["options"] = options
    return p


def row(title, y):
    return {"type": "row", "title": title, "collapsed": False,
            "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


def text(pid, x, y, w, h, content, title=""):
    return {"id": pid, "title": title, "type": "text",
            "gridPos": {"x": x, "y": y, "w": w, "h": h},
            "options": {"mode": "markdown", "content": content}}


panels = []
y = 0

panels.append(text(1, 0, y, 24, 5, """# AI Productivity Funnel

*Six stages, each measured on its own. Adoption is not productivity, and this
is laid out so the two cannot be mistaken for each other.*

**The stages do not multiply.** 70% adoption × 80% engagement × 60% assisted
work is not a productivity figure — it is arithmetic on unrelated
denominators. Read each stage against its own question.
"""))
y += 5

# ---------------------------------------------------------------- adoption
panels.append(row("Adoption funnel — are we adopting AI?", y)); y += 1

panels.append(panel(
    10, "Stage 1 · Adoption", "bargauge", 0, y, 12, 5,
    SQL["Stage 1 · Adoption"],
    color=CONTINUOUS_BLUES, novalue="no contributors recorded",
    description=(
        "How many people used an agent at all, against how many are known "
        "to this tool.\n\n"
        "The denominator is contributors with a recorded identity — a "
        "repository instrumented without one is invisible here, which is "
        "worth knowing before reading the ratio as coverage."),
    options=BARGAUGE))

panels.append(panel(
    11, "Stage 2 · Engagement — how deep, not how many", "bargauge", 12, y, 12, 5,
    SQL["Stage 2 · Engagement — how deep, not how many"],
    color=CONTINUOUS_BLUES, novalue="no sessions in range",
    description=(
        "Sessions bucketed by what actually happened in them, from "
        "conversation-only up to agentic work using MCP or many tools.\n\n"
        "Depth rather than count, deliberately: ten sessions that produced "
        "nothing and one that rewrote a subsystem are not the same "
        "adoption, and a session count treats them identically."),
    options=BARGAUGE))
y += 5

panels.append(panel(
    12, "Habitual use (WAU / MAU)", "stat", 0, y, 12, 4,
    SQL["Habitual use (WAU / MAU)"],
    unit="percent", decimals=0, color=NEUTRAL,
    novalue="no sessions in range",
    description=(
        "Weekly actives over monthly actives — whether use is habitual or "
        "occasional.\n\n"
        "A high ratio means the people who use it use it regularly. It says "
        "nothing about how many people that is, which is stage 1."),
    options=STAT))

panels.append(panel(
    13, "Sessions doing real work", "stat", 12, y, 12, 4,
    SQL["Sessions doing real work"],
    unit="percent", decimals=0, color=NEUTRAL,
    novalue="no sessions in range",
    description=(
        "Share of sessions that edited a file, as opposed to conversation "
        "that produced no change.\n\n"
        "Conversation is not waste — planning and reading are work. This "
        "measures how much of the tool's use reached the codebase, not how "
        "much of it was useful."),
    options=STAT))
y += 4

# ------------------------------------------------------------------- value
panels.append(row("Value funnel — is AI producing value?", y)); y += 1

panels.append(panel(
    20, "Stage 3 · AI-assisted work", "bargauge", 0, y, 12, 5,
    SQL["Stage 3 · AI-assisted work"],
    color=CONTINUOUS_BLUES, novalue="no commits in range",
    description=(
        "Share of commits carrying evidence of agent involvement.\n\n"
        "Undetermined is not 'no AI was used' — it is 'no evidence either "
        "way'. A commit made before the hooks were installed, or on a "
        "machine without them, lands there regardless of how it was "
        "written (NAV-21)."),
    options=BARGAUGE))

panels.append(panel(
    21, "Stage 4 · Cycle time — assisted vs not", "bargauge", 12, y, 12, 5,
    SQL["Stage 4 · Cycle time — assisted vs not"],
    color=CONTINUOUS_GREENS, novalue="no PRs with both states in range",
    description=(
        "Median PR cycle time, split by whether the work was assisted.\n\n"
        "**Not a controlled comparison.** An agent is reached for on some "
        "kinds of work and not others, so part of any gap measures which "
        "work was assigned rather than what the agent did to it. Read it as "
        "a difference between two populations, not as an effect."),
    options=BARGAUGE))
y += 5

# Stage 5 now has content, which it did not before.
panels.append(panel(
    22, "Stage 5 · Cost per delivered line", "stat", 0, y, 6, 5,
    f"""SELECT ROUND(
  (SELECT SUM(s.output_tokens)
     FROM whodunit_sessions s JOIN whodunit_repos r2 ON r2.repo_id = s.repo_id
    WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR.replace('r.', 'r2.')})
  / NULLIF((SELECT SUM(c.lines_added + c.lines_removed)
     FROM whodunit_commits c JOIN whodunit_repos r3 ON r3.repo_id = c.repo_id
    WHERE c.status = 'assisted' AND {CONTRIBUTOR.replace('r.', 'r3.')}), 0)
) AS v""",
    decimals=0, color=NEUTRAL,
    novalue="no tokens or no assisted lines recorded",
    description=(
        "Output tokens spent per line of assisted work that reached a "
        "commit.\n\n"
        "**This is output per unit of effort — stage 5's actual question — "
        "and it is not a productivity gain.** It has no before to compare "
        "against; it says what the work cost, not whether that is better "
        "than the alternative.\n\n"
        "Tokens, never currency: under a subscription the marginal cost of "
        "a token is zero, so a price table would report money nobody "
        "spent."),
    options=STAT))

panels.append(panel(
    23, "Stage 5 · Rework rate, assisted", "stat", 6, y, 6, 5,
    f"""SELECT ROUND(100.0 * SUM(c.purpose = 'fix' AND c.status = 'assisted')
  / NULLIF(SUM(c.status = 'assisted'), 0), 1) AS v
FROM whodunit_commits c JOIN whodunit_repos r ON r.repo_id = c.repo_id
WHERE {CONTRIBUTOR}""",
    unit="percent", decimals=1, color=NEUTRAL,
    novalue="no assisted commits in range",
    description=(
        "Share of assisted commits labelled as fixes — a rework proxy.\n\n"
        "**Not a defect rate.** `purpose` comes from Conventional Commit "
        "prefixes and path heuristics, so it measures what commits were "
        "labelled, not what broke.\n\n"
        "**Read the banded version on the delivery dashboard before "
        "concluding anything.** Unbanded, assisted work looks better than "
        "unassisted (28.4% against 33.3%); split by change size that "
        "reverses in the middle band. The single figure here is a size-mix "
        "artefact and is shown as an entry point, not an answer."),
    options=STAT))

panels.append(panel(
    24, "Stage 5 · Pre-adoption baseline", "stat", 12, y, 6, 5,
    f"""SELECT CONCAT(b.commits, ' commits / ', b.window_days, 'd') AS v
FROM whodunit_baselines b JOIN whodunit_repos r ON r.repo_id = b.repo_id
WHERE {CONTRIBUTOR}
ORDER BY b.captured_at DESC LIMIT 1""",
    color=NEUTRAL,
    novalue="no baseline captured for this repository",
    description=(
        "The pre-adoption window this repository can be compared against — "
        "captured by `dun baseline capture` and published by `dun sync` "
        "(NAV-107).\n\n"
        "**A pre-instrumentation baseline is not a no-AI baseline.** "
        "Commits in that window may have been AI-assisted and simply not "
        "recorded, so it is a comparison against 'before we measured', not "
        "against 'before we used it'.\n\n"
        "The window is fixed at capture and immutable afterwards, because a "
        "window chosen after seeing the result can manufacture almost any "
        "figure.\n\n"
        "Empty means no baseline was captured — not that the repository had "
        "no activity before instrumentation."),
    options=STAT))

panels.append(text(25, 18, y, 6, 5, """### Stage 6 · Business value

**Not measurable here, and that is not a gap to be closed.**

Revenue, customer outcomes and time-to-market are the things a business
actually cares about. Nothing in a git history or an agent transcript
reaches them, and any number this tool produced for them would be
invented.

The stage stays on the funnel because deleting it would imply the funnel
ends at stage 5 — and that stage 5 is the answer to "was it worth it".
It is not.
"""))
y += 5

# What is still missing, stated rather than implied.
panels.append(text(26, 0, y, 24, 4, """### What stage 5 still cannot say

The three panels above measure **cost and rework**, not gain. A productivity
gain needs the same work done both ways, and this data does not contain that:

- **Selection is not random.** An agent is reached for on some kinds of work
  and not others, so assisted and unassisted commits are different populations.
- **The baseline is pre-instrumentation, not pre-AI.** Commits before the hooks
  went in may well have been assisted and simply unrecorded.
- **Change size is not output.** A larger commit may be more work done, or more
  code written for the same outcome.

Cost per delivered unit and rework rate are the honest answers available. A
percentage would not be.
"""))
y += 4

dashboard = {
    "uid": "whodunit-funnel",
    "title": "Whodunit — AI Productivity Funnel",
    "tags": ["whodunit", "adoption"],
    "timezone": "browser",
    "schemaVersion": 39,
    "version": 1,
    "refresh": "",
    "time": {"from": "now-90d", "to": "now"},
    "templating": {"list": [
        {"name": "datasource", "type": "datasource", "query": "mysql",
         "current": {"selected": True, "text": "mysql", "value": "mysql"},
         "hide": 0, "label": "Data source"},
        {"name": "contributor", "type": "query", "datasource": "${datasource}",
         "label": "Contributor",
         "query": "SELECT DISTINCT COALESCE(NULLIF(contributor, ''), '(unattributed)') AS __text, COALESCE(NULLIF(contributor, ''), '') AS __value FROM whodunit_repos ORDER BY 1",
         "current": {"selected": True, "text": "All", "value": "__all__"},
         # multi=false stated explicitly. With includeAll set and multi
         # absent, Grafana treats the variable as multi-capable and
         # substitutes a selected value as `{value}` — which matches
         # nothing, so every panel empties the moment a contributor is
         # picked while "All" keeps working, because '__all__' is
         # compared literally.
         "multi": False, "includeAll": True, "allValue": "__all__", "refresh": 1, "hide": 0},
    ]},
    "panels": panels,
}

out = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                   "dashboards", "whodunit-funnel.json")
with open(out, "w") as f:
    json.dump(dashboard, f, indent=2)
    f.write("\n")
print(f"wrote {out}: {len(panels)} panels")
