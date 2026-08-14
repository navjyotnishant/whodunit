#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: Generates whodunit-cost.json — token use, cache efficiency,
# model and branch attribution.
"""Build the cost & efficiency dashboard.

Generated rather than hand-written because the SQL is the interesting part
and it repeats: every panel carries the same contributor filter and the
same nanosecond timestamp conversion, and a 22-panel JSON file hand-edited
twice drifts. The panels are declared below as data; the JSON is an output.

Run `./build-cost-dashboard.py` then `./export-dashboards.py` — the second
regenerates the importable copy, and CI checks the two agree.

## The two numbers that decide whether this dashboard is honest

**Cache writes count as uncached in the read ratio.** A write arrives
uncached and is billed above base rate, so leaving it out of the
denominator turns a real 48% into a flattering 99% — measured, on this
project's own data. A panel showing 99% recommends nothing, so the
flattering error is also the one that makes the dashboard useless.

**Break-even for write amortisation is 1.25x, not 1.0x.** A write costs
about 1.25x base and a read 0.1x, so a write needs roughly 1.25 reads
before it pays for itself. A series sitting at 1.10x lost money while
looking fine against a 1.0 line.

## NAV-21 throughout

Every panel that can be structurally empty for an agent says so rather
than rendering zero. agy reports no tokens and no timing at all; Codex
reports cache reads but never writes. A zero on a cost panel reads as
"this agent is free" — so the SQL filters to rows where the measurement
exists, and `noValue` explains the emptiness.
"""

import json
import os

# Timestamps are stored as Unix nanoseconds, so every date comparison
# multiplies out. Named once rather than repeated in fifteen queries.
NS = "* 1000000000"
CONTRIBUTOR = "('$contributor' = '__all__' OR r.contributor = '$contributor')"

# A write costs ~1.25x base, a read ~0.1x. Below this ratio the write cost
# more than it saved. See the module docstring.
BREAK_EVEN = 1.25


def panel(pid, title, ptype, x, y, w, h, sql, *, unit=None, decimals=None,
          novalue=None, description=None, options=None, overrides=None,
          thresholds=None, minval=None):
    p = {
        "id": pid,
        "title": title,
        "type": ptype,
        "datasource": "${datasource}",
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [{"refId": "A", "format": "table", "rawSql": sql}],
        "fieldConfig": {"defaults": {}, "overrides": overrides or []},
    }
    if description:
        p["description"] = description
    d = p["fieldConfig"]["defaults"]
    if unit:
        d["unit"] = unit
    if decimals is not None:
        d["decimals"] = decimals
    if novalue:
        d["noValue"] = novalue
    if minval is not None:
        d["min"] = minval
    if thresholds:
        d["thresholds"] = {"mode": "absolute", "steps": thresholds}
    if options:
        p["options"] = options
    return p


def row(title, y):
    return {"type": "row", "title": title, "collapsed": False,
            "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


STAT = {"graphMode": "none", "textMode": "value",
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}}

panels = []
y = 0

# ---------------------------------------------------------------- headline
panels.append(row("What did it cost — in tokens", y)); y += 1

panels.append(panel(
    100, "Total tokens", "stat", 0, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.input_tokens + s.output_tokens
       + COALESCE(s.cache_read_tokens,0) + COALESCE(s.cache_write_tokens,0)), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", decimals=1, novalue="no session reported tokens",
    description=(
        "Every token billed: uncached input + cache reads + cache writes + "
        "output.\n\n"
        "**Almost all of it is cache reads.** Measured here, output is 0.1% "
        "of the total — so this number tracks how much context was re-sent, "
        "not how much the models produced. The tiles beside it break it "
        "down, because a single figure in the billions invites the wrong "
        "conclusion.\n\n"
        "Tokens, not currency: under a subscription the marginal cost of a "
        "token is zero, so a price table would report money nobody spent. "
        "Multiply by your own contract if you need a figure."),
    options=STAT))

panels.append(panel(
    101, "Output tokens", "stat", 4, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.output_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", decimals=1, novalue="not reported",
    description=(
        "What the models actually produced, as opposed to what was re-sent "
        "to them.\n\n"
        "Typically a fraction of a percent of the total — 0.1% measured "
        "here. The two tiles carry different units at these magnitudes "
        "(billions against millions), so read the share below rather than "
        "comparing them by eye."),
    options=STAT))

panels.append(panel(
    106, "Uncached input", "stat", 8, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.input_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", decimals=1, novalue="not reported",
    description=(
        "Input the model had to read fresh, at full price.\n\n"
        "This is the figure a cache is meant to shrink. It was missing from "
        "this dashboard entirely, which left the headline total — almost all "
        "of it cache reads — looking like the whole story."),
    options=STAT))

panels.append(panel(
    107, "Cache reads", "stat", 12, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.cache_read_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.cache_read_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", decimals=1, novalue="not reported",
    description=(
        "Context re-sent and served from cache at about a tenth of base "
        "rate.\n\n"
        "Normally the largest number on this dashboard by three orders of "
        "magnitude — 99% of the total here — which is why the headline "
        "figure is not a measure of how much work was produced."),
    options=STAT))

panels.append(panel(
    102, "Served from cache", "stat", 16, y, 4, 4,
    f"""SELECT ROUND(100.0 * SUM(COALESCE(s.cache_read_tokens,0))
  / NULLIF(SUM(s.input_tokens + COALESCE(s.cache_read_tokens,0)
             + COALESCE(s.cache_write_tokens,0)), 0), 1) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="percent", decimals=1, novalue="no input tokens recorded",
    description=(
        "Share of input tokens served from cache.\n\n"
        "**Cache writes count as uncached here**, because a write arrives "
        "uncached and is billed above base rate. Omitting them turns a real "
        "48% into a flattering 99% — measured on this project's own data."),
    thresholds=[{"color": "orange", "value": None},
                {"color": "green", "value": 50}],
    options=STAT))

panels.append(panel(
    103, "Cache write payback", "stat", 20, y, 4, 4,
    f"""SELECT ROUND(SUM(COALESCE(s.cache_read_tokens,0))
  / NULLIF(SUM(s.cache_write_tokens), 0), 2) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.cache_write_tokens IS NOT NULL AND s.cache_write_tokens > 0 AND {CONTRIBUTOR}""",
    decimals=2, novalue="no agent reported cache writes",
    description=(
        f"Reads returned per token written to cache. Below {BREAK_EVEN}x the "
        "writes cost more than they saved — a write is billed at about 1.25x "
        "base and a read at 0.1x.\n\n"
        "**The obvious break-even of 1.0 is wrong**: a series at 1.10x lost "
        "money while looking healthy.\n\n"
        "Empty for Codex, which reports cache reads but never writes."),
    thresholds=[{"color": "red", "value": None},
                {"color": "green", "value": BREAK_EVEN}],
    options=STAT))

panels.append(panel(
    104, "Sessions with cost data", "stat", 0, y + 4, 6, 3,
    f"""SELECT CONCAT(
  SUM(CASE WHEN s.input_tokens IS NOT NULL THEN 1 ELSE 0 END),
  ' of ', COUNT(*)) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE {CONTRIBUTOR}""",
    novalue="no sessions",
    description=(
        "The denominator, shown because a total is meaningless without it.\n\n"
        "Antigravity reports no tokens at all, so a repository using it will "
        "never reach 100% — that is a property of the agent, not a gap to be "
        "filled."),
    options={**STAT, "textMode": "value"}))

panels.append(panel(
    105, "Reasoning tokens", "stat", 6, y + 4, 6, 3,
    f"""SELECT COALESCE(SUM(s.reasoning_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.reasoning_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", decimals=1, novalue="only Codex separates these",
    description=(
        "Tokens spent thinking rather than answering.\n\n"
        "Codex alone reports this. Claude Code and Antigravity do not "
        "separate it, so an empty panel here means 'not reported', not zero."),
    options=STAT))

# The composition pie sits on the same second row as the two tiles
# above, so its y is captured before the cursor advances.
PIE_Y = y + 4
y += 7

panels.append(panel(
    108, "What the tokens are", "piechart", 12, PIE_Y, 12, 3,
    f"""SELECT 'Cache read' AS metric, SUM(COALESCE(s.cache_read_tokens,0)) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
UNION ALL
SELECT 'Uncached input', SUM(s.input_tokens)
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
UNION ALL
SELECT 'Cache write', SUM(COALESCE(s.cache_write_tokens,0))
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
UNION ALL
SELECT 'Output', SUM(s.output_tokens)
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit="short", novalue="no session reported tokens",
    description=(
        "The headline total, broken into its parts.\n\n"
        "Without this, a total in the billions beside an output in the "
        "millions reads as a contradiction — the units differ by a factor "
        "of a thousand and the eye does not correct for that. Here the "
        "answer is visible: cache reads dominate, output is a sliver.\n\n"
        "A cache read is billed at roughly a tenth of base rate and a write "
        "at 1.25x, so the largest slice is not the most expensive one."),
    options={"legend": {"displayMode": "table", "placement": "right",
                        "values": ["value", "percent"]},
             "reduceOptions": {"calcs": ["lastNotNull"], "values": True},
             "pieType": "donut"}))

# ------------------------------------------------------------- per model
panels.append(row("Per model — where the aggregate hides a loss", y)); y += 1

panels.append(panel(
    110, "Token use and cache efficiency by model", "table", 0, y, 24, 9,
    f"""SELECT
  COALESCE(NULLIF(s.model, ''), '(unattributed)') AS Model,
  COUNT(*)                                        AS Sessions,
  SUM(s.input_tokens)                             AS `Uncached in`,
  SUM(s.output_tokens)                            AS `Out`,
  SUM(COALESCE(s.cache_read_tokens,0))            AS `Cache read`,
  SUM(COALESCE(s.cache_write_tokens,0))           AS `Cache write`,
  ROUND(100.0 * SUM(COALESCE(s.cache_read_tokens,0))
    / NULLIF(SUM(s.input_tokens + COALESCE(s.cache_read_tokens,0)
               + COALESCE(s.cache_write_tokens,0)), 0), 1) AS `From cache %`,
  ROUND(SUM(COALESCE(s.cache_read_tokens,0))
    / NULLIF(SUM(s.cache_write_tokens), 0), 2)    AS `Write payback`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
GROUP BY COALESCE(NULLIF(s.model, ''), '(unattributed)')
ORDER BY SUM(s.input_tokens + s.output_tokens
           + COALESCE(s.cache_read_tokens,0)
           + COALESCE(s.cache_write_tokens,0)) DESC""",
    novalue="no session reported tokens",
    description=(
        "**Per model, because the aggregate hides the finding.** An overall "
        f"48% read ratio measured on real data contained one model at 0.73x "
        "amortisation — a loss, invisible in the total.\n\n"
        f"`Write payback` below {BREAK_EVEN}x is red: those writes cost more "
        "than they saved. Empty means the agent does not report cache writes "
        "rather than that it wrote none."),
    overrides=[
        {"matcher": {"id": "byName", "options": "Write payback"},
         "properties": [
             {"id": "custom.cellOptions",
              "value": {"type": "color-text"}},
             {"id": "thresholds",
              "value": {"mode": "absolute", "steps": [
                  {"color": "red", "value": None},
                  {"color": "green", "value": BREAK_EVEN}]}},
             {"id": "noValue", "value": "not reported"}]},
        {"matcher": {"id": "byName", "options": "From cache %"},
         "properties": [{"id": "unit", "value": "percent"},
                        {"id": "decimals", "value": 1}]},
        {"matcher": {"id": "byRegexp", "options": ".*(in|Out|read|write)$"},
         "properties": [{"id": "unit", "value": "short"},
                        {"id": "decimals", "value": 1}]},
    ],
    options={"showHeader": True,
             "sortBy": [{"displayName": "Uncached in", "desc": True}]}))
y += 9

panels.append(panel(
    111, "Output tokens per model over time", "timeseries", 0, y, 12, 8,
    f"""SELECT
  FROM_UNIXTIME(FLOOR(s.last_seen / 1000000000 / 86400) * 86400) AS time,
  COALESCE(NULLIF(s.model, ''), '(unattributed)')                AS metric,
  SUM(s.output_tokens)                                           AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}
  AND s.last_seen >= $__unixEpochFrom() {NS}
  AND s.last_seen <  $__unixEpochTo() {NS}
GROUP BY 1, 2 ORDER BY 1""",
    unit="short", novalue="no tokens in this range",
    description="Which model is doing the work, and when that changed."))

panels.append(panel(
    112, "Cache write payback by model", "timeseries", 12, y, 12, 8,
    f"""SELECT
  FROM_UNIXTIME(FLOOR(s.last_seen / 1000000000 / 86400) * 86400) AS time,
  COALESCE(NULLIF(s.model, ''), '(unattributed)')                AS metric,
  SUM(COALESCE(s.cache_read_tokens,0)) / NULLIF(SUM(s.cache_write_tokens), 0) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.cache_write_tokens IS NOT NULL AND s.cache_write_tokens > 0 AND {CONTRIBUTOR}
  AND s.last_seen >= $__unixEpochFrom() {NS}
  AND s.last_seen <  $__unixEpochTo() {NS}
GROUP BY 1, 2 ORDER BY 1""",
    decimals=2, minval=0, novalue="no agent reported cache writes",
    description=(
        f"Break-even is {BREAK_EVEN}x, drawn as a threshold. A line below it "
        "is a model whose caching is costing money.\n\n"
        "Measured spreads run from 0.73x to 21x, so the axis is generous — a "
        "linear scale flattens the low end, which is the end that matters."),
    thresholds=[{"color": "red", "value": None},
                {"color": "green", "value": BREAK_EVEN}]))
y += 8

# ------------------------------------------------------- branch & autonomy
panels.append(row("Where it landed, and how much rope the agent had", y)); y += 1

panels.append(panel(
    120, "Agent-written lines by branch", "table", 0, y, 12, 8,
    f"""SELECT
  e.branch                        AS Branch,
  COUNT(*)                        AS Events,
  SUM(e.lines_added)              AS `Lines added`,
  COUNT(DISTINCT e.session)       AS Sessions
FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
WHERE e.branch IS NOT NULL AND {CONTRIBUTOR}
GROUP BY e.branch ORDER BY SUM(e.lines_added) DESC LIMIT 25""",
    novalue="no event recorded a branch",
    description=(
        "Antigravity records no branch at all — its branch-like strings are a "
        "tracker's *suggested* branch inside an MCP response, not the "
        "checked-out one. Its events are absent here rather than grouped "
        "under a wrong name."),
    options={"showHeader": True}))

panels.append(panel(
    121, "Autonomy granted", "table", 12, y, 12, 8,
    f"""SELECT
  s.permission_mode         AS `Permission mode`,
  s.agent                   AS Agent,
  COUNT(*)                  AS Sessions,
  SUM(s.tool_calls)         AS `Tool calls`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.permission_mode IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.permission_mode, s.agent ORDER BY COUNT(*) DESC""",
    novalue="no session recorded a permission mode",
    description=(
        "How much the agent was allowed to do unattended.\n\n"
        "Each agent's own vocabulary is preserved rather than mapped onto a "
        "shared enum — Codex calls it an approval policy, Claude Code a "
        "permission mode, and guessing at equivalences would invent a "
        "comparison neither agent made."),
    options={"showHeader": True}))
y += 8

# --------------------------------------------------------- session shape
panels.append(row("Session shape — the actionable half", y)); y += 1

panels.append(panel(
    130, "Human edited the agent's output", "stat", 0, y, 6, 4,
    f"""SELECT ROUND(100.0 * SUM(CASE WHEN e.user_modified = 1 THEN 1 ELSE 0 END)
  / NULLIF(COUNT(*), 0), 1) AS v
FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
WHERE e.user_modified IS NOT NULL AND {CONTRIBUTOR}""",
    unit="percent", decimals=1, novalue="only Claude Code reports this",
    description=(
        "Share of edits a human changed before committing — the difference "
        "between 'the agent wrote this' and 'the agent wrote this and it was "
        "kept'.\n\n"
        "Claude Code alone reports it. The denominator counts only calls that "
        "carried the signal, so agents without it do not dilute the rate."),
    options=STAT))

panels.append(panel(
    131, "Longest sessions", "table", 6, y, 9, 4,
    f"""SELECT
  s.agent                                            AS Agent,
  COALESCE(NULLIF(s.model, ''), '—')                 AS Model,
  ROUND((s.last_seen - s.first_seen) / 1000000000 / 3600, 1) AS `Hours`,
  s.tool_calls                                       AS `Tool calls`,
  s.output_tokens                                    AS `Out tokens`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE {CONTRIBUTOR}
  AND s.last_seen > s.first_seen
  -- A session whose first record predates the ingest window carries the
  -- zero time, which reaches the database as -6795364578871345152 and
  -- renders as a 272-year session. The adapter no longer writes those,
  -- but a database synced before that fix still holds them, and one such
  -- row at the top of a DESC sort makes the whole panel nonsense.
  AND s.first_seen > 0
ORDER BY (s.last_seen - s.first_seen) DESC LIMIT 10""",
    novalue="no sessions with a usable duration",
    description=(
        "A long session costs more even when cached, because the whole "
        "context is re-sent each turn. Shown as a list rather than an average "
        "— a mean hides the few sessions that are the problem.\n\n"
        "Rows whose start time was never recorded are excluded rather than "
        "shown as impossibly long."),
    options={"showHeader": True}))

panels.append(panel(
    132, "Turn latency", "stat", 15, y, 5, 4,
    f"""SELECT ROUND(AVG(s.duration_ms) / 1000, 1) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.duration_ms IS NOT NULL AND {CONTRIBUTOR}""",
    unit="s", decimals=1, novalue="only Codex records timing",
    description=(
        "Codex alone records per-turn timing. Claude Code and Antigravity "
        "report none, so this is empty for them rather than zero — a zero "
        "would make them the fastest agents on this panel."),
    options=STAT))

panels.append(panel(
    133, "Reasoning effort", "piechart", 20, y, 4, 4,
    f"""SELECT s.effort AS metric, COUNT(*) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.effort IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.effort""",
    novalue="no session recorded an effort tier",
    description="How hard the model was asked to think, where the agent says.",
    options={"legend": {"displayMode": "list", "placement": "bottom"},
             "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}))
y += 4

# ------------------------------------------------------------ MCP servers
panels.append(row("MCP", y)); y += 1

panels.append(panel(
    140, "Calls per MCP server", "barchart", 0, y, 12, 7,
    f"""SELECT e.mcp_server AS metric, COUNT(*) AS value
FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
WHERE e.mcp_server IS NOT NULL AND {CONTRIBUTOR}
GROUP BY e.mcp_server ORDER BY COUNT(*) DESC LIMIT 15""",
    novalue="no MCP calls recorded",
    description=(
        "Which servers the agents actually reach for.\n\n"
        "Antigravity is absent: what looked like MCP identity in its store "
        "turned out to be the command text being approved, which is content "
        "rather than an identifier."),
    options={"legend": {"showLegend": False},
             "orientation": "horizontal"}))

panels.append(panel(
    141, "Tokens per session, by agent", "table", 12, y, 12, 7,
    f"""SELECT
  s.agent                                         AS Agent,
  COUNT(*)                                        AS Sessions,
  ROUND(AVG(s.output_tokens))                     AS `Avg out`,
  ROUND(AVG(s.input_tokens + COALESCE(s.cache_read_tokens,0)
          + COALESCE(s.cache_write_tokens,0)))    AS `Avg in`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.agent ORDER BY AVG(s.output_tokens) DESC""",
    novalue="no session reported tokens",
    description=(
        "**Not a comparison of which agent is cheaper.** Sessions are not "
        "matched work: each agent draws different tasks, and a model reached "
        "for when the job is hard will always look more expensive. This says "
        "what each cost on the work it happened to do, nothing more."),
    overrides=[{"matcher": {"id": "byRegexp", "options": "Avg.*"},
                "properties": [{"id": "unit", "value": "short"},
                               {"id": "decimals", "value": 1}]}],
    options={"showHeader": True}))
y += 7

dashboard = {
    "uid": "whodunit-cost",
    "title": "Whodunit — Cost & Efficiency",
    "tags": ["whodunit", "cost"],
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
         "query": "SELECT DISTINCT contributor FROM whodunit_repos WHERE contributor <> ''",
         "current": {"selected": True, "text": "All", "value": "__all__"},
         "includeAll": True, "allValue": "__all__", "refresh": 1, "hide": 0},
    ]},
    "panels": panels,
}

out = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                   "dashboards", "whodunit-cost.json")
with open(out, "w") as f:
    json.dump(dashboard, f, indent=2)
    f.write("\n")
print(f"wrote {out}: {len(panels)} panels")
