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
# Grafana's "short" unit spells magnitudes as Bil/Mil/K. "sishort" gives
# B/M/k, which is what people read a token count in — and it keeps the
# tiles narrow enough that six fit across a row.
TOKEN_UNIT = "sishort"

NS = "* 1000000000"
CONTRIBUTOR = "('$contributor' = '__all__' OR r.contributor = '$contributor')"

# A write costs ~1.25x base, a read ~0.1x. Below this ratio the write cost
# more than it saved. See the module docstring.
BREAK_EVEN = 1.25


def panel(pid, title, ptype, x, y, w, h, sql, *, unit=None, decimals=None,
          novalue=None, description=None, options=None, overrides=None,
          thresholds=None, minval=None, fmt="table"):
    p = {
        "id": pid,
        "title": title,
        "type": ptype,
        "datasource": "${datasource}",
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [{"refId": "A", "format": fmt, "rawSql": sql}],
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
    unit=TOKEN_UNIT, decimals=1, novalue="no session reported tokens",
    description=(
        "Every token billed: uncached input + cache reads + cache writes + "
        "output.\n\n"
        "**Almost all of it is cache reads.** Measured here: 7.53B cache "
        "reads, 54.6M cache writes, 13.2M uncached input, 7.9M output — so "
        "this number tracks how much context was re-sent, not how much the "
        "models produced. The four tiles to the right hold the same "
        "breakdown as live figures.\n\n"
        "Tokens, not currency: under a subscription the marginal cost of a "
        "token is zero, so a price table would report money nobody spent. "
        "Multiply by your own contract if you need a figure."),
    options=STAT))

panels.append(panel(
    101, "Output tokens", "stat", 4, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.output_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit=TOKEN_UNIT, decimals=1, novalue="not reported",
    description=(
        "What the models actually produced, as opposed to what was re-sent "
        "to them.\n\n"
        "Typically a fraction of a percent of the total — 0.1% measured "
        "here.\n\n"
        "**Total and Output are three orders of magnitude apart**, so they "
        "render in different units (B against M) and comparing them by eye "
        "reads as a contradiction. They are not: 7.6 B is a thousand times "
        "7.6 M."),
    options=STAT))

panels.append(panel(
    106, "Uncached input", "stat", 8, y, 4, 4,
    f"""SELECT COALESCE(SUM(s.input_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit=TOKEN_UNIT, decimals=1, novalue="not reported",
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
    unit=TOKEN_UNIT, decimals=1, novalue="not reported",
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
    105, "Reasoning tokens", "stat", 0, y + 4, 6, 4,
    f"""SELECT COALESCE(SUM(s.reasoning_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.reasoning_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit=TOKEN_UNIT, decimals=1, novalue="only Codex separates these",
    description=(
        "Tokens spent thinking rather than answering.\n\n"
        "Codex alone reports this. Claude Code and Antigravity do not "
        "separate it, so an empty panel here means 'not reported', not zero."),
    options=STAT))

# The three signals below sit on the headline's second row rather than in
# a section of their own further down.
#
# They answer "what did this cost us" as directly as the token counts do —
# effort is the setting that drives token spend, latency is the wait it
# buys, and the human-edited share is whether the output survived contact
# with a reviewer. A reader who only looks at the top of the page should
# see them.
SHAPE_Y = y + 4
y += 8

panels.append(panel(
    130, "Human edited the agent's output", "stat", 6, SHAPE_Y, 6, 4,
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
    132, "Turn latency", "stat", 12, SHAPE_Y, 6, 4,
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
    133, "Reasoning effort", "piechart", 18, SHAPE_Y, 6, 4,
    f"""SELECT s.effort AS metric, COUNT(*) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.effort IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.effort""",
    novalue="no session recorded an effort tier",
    description=(
        "How hard the model was asked to think, where the agent says.\n\n"
        "The lever behind the token counts above: a higher tier spends more "
        "on the same task."),
    options={"legend": {"displayMode": "list", "placement": "bottom"},
             "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}))

# ------------------------------------------------------------- per model
panels.append(row("Per model — where the aggregate hides a loss", y)); y += 1

panels.append(panel(
    109, "Output tokens by model", "bargauge", 0, y, 24, 5,
    f"""SELECT
  COALESCE(NULLIF(s.model, ''), '(unattributed)') AS metric,
  SUM(s.output_tokens)                            AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}
GROUP BY 1 ORDER BY 2 DESC""",
    unit=TOKEN_UNIT, decimals=1, novalue="no session reported tokens",
    description=(
        "Which model produced the most, ranked.\n\n"
        "A bar gauge because this is one metric across categories, which is "
        "the only shape it reads well. The table below carries the numbers "
        "that only mean something together — a 153x payback looks impressive "
        "until you see it came from 34 sessions.\n\n"
        "Deliberately NOT a bar gauge of cache payback: three of seven models "
        "here report no cache writes at all, so that chart would render four "
        "bars and silently drop the rest. A table can show \"not reported\"; "
        "a bar cannot (NAV-21)."),
    options={"displayMode": "gradient", "orientation": "horizontal",
             "showUnfilled": True, "valueMode": "text",
             "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}))

panels.append(panel(
    110, "Token use and cache efficiency by model", "table", 0, y + 5, 24, 9,
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
        # Rendered as in-cell bars rather than bare digits. Eight columns
        # of raw counts is a wall of numbers; the bar makes the relative
        # sizes readable at a glance while the value stays exact, which a
        # bar gauge on its own would lose.
        {"matcher": {"id": "byRegexp", "options": ".*(in|Out|read|write)$"},
         "properties": [{"id": "unit", "value": TOKEN_UNIT},
                        {"id": "decimals", "value": 1},
                        {"id": "custom.cellOptions",
                         "value": {"type": "gauge", "mode": "gradient",
                                   "valueDisplayMode": "text"}}]},
        {"matcher": {"id": "byName", "options": "Sessions"},
         "properties": [{"id": "custom.cellOptions",
                         "value": {"type": "gauge", "mode": "lcd",
                                   "valueDisplayMode": "text"}}]},
    ],
    options={"showHeader": True,
             "sortBy": [{"displayName": "Uncached in", "desc": True}]}))
# 5 for the bar gauge above, 9 for the table itself.
y += 14

panels.append(panel(
    111, "Output tokens per model over time", "timeseries", 0, y, 24, 8,
    f"""SELECT
  FROM_UNIXTIME(FLOOR(s.last_seen / 1000000000 / 86400) * 86400) AS time,
  COALESCE(NULLIF(s.model, ''), '(unattributed)')                AS metric,
  SUM(s.output_tokens)                                           AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.output_tokens IS NOT NULL AND {CONTRIBUTOR}
  AND s.last_seen >= $__unixEpochFrom() {NS}
  AND s.last_seen <  $__unixEpochTo() {NS}
GROUP BY 1, 2 ORDER BY 1""",
    unit=TOKEN_UNIT, novalue="no tokens in this range",
    fmt="time_series",
    description=(
        "Which model is doing the work, and when that changed.\n\n"
        "One line per model, named and coloured. That needs "
        "`format: time_series`, which is what turns a `metric` column into "
        "separate series — with `format: table` Grafana returns one frame "
        "with a literal metric column and labels every line \"value\", so "
        "the models are indistinguishable.\n\n"
        "Full width because the cache-payback chart that used to sit beside "
        "it was removed: payback is a static per-model fact here, not a "
        "trend. Three of seven models never report cache writes at all and "
        "the rest sit at 47-153x, so plotting it over time drew four flat "
        "lines nowhere near the break-even the chart existed to mark. The "
        "number itself stays in the table below, where its session count is "
        "beside it — 153x reads very differently on 34 sessions than on 1."),
    options={"legend": {"displayMode": "table", "placement": "bottom",
                        "showLegend": True, "calcs": ["sum"]},
             "tooltip": {"mode": "multi", "sort": "desc"}}))
y += 8

# ------------------------------------------------------- branch & autonomy
panels.append(row("Where it landed, how much rope, and how long", y)); y += 1

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
    121, "Autonomy granted", "barchart", 12, y, 12, 8,
    f"""SELECT
  CONCAT(s.permission_mode, '  (', s.agent, ')') AS mode,
  COUNT(*)                                       AS Sessions,
  SUM(s.tool_calls)                              AS `Tool calls`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.permission_mode IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.permission_mode, s.agent
ORDER BY SUM(s.tool_calls) DESC""",
    novalue="no session recorded a permission mode",
    description=(
        "How much the agent was allowed to do unattended, and how much work "
        "it did with that rope.\n\n"
        "**Sessions and tool calls run opposite here**, which is the point of "
        "charting it: measured on this data, 64 sessions on Codex's `never` "
        "produced 506 tool calls, while 2 Claude Code sessions on `auto` "
        "produced 6,766. Autonomy is not how often it is granted, it is how "
        "much happens once it is.\n\n"
        "The agent is in the label rather than a separate series because each "
        "mode belongs to exactly one agent — Codex says `never` and "
        "`on-request`, Claude Code says `acceptEdits`, `auto` and `default`. "
        "Grouping by both would draw a chart where every group is one bar "
        "tall.\n\n"
        "Each agent's own vocabulary is kept rather than mapped onto a shared "
        "enum: guessing that `never` means the same as `default` would invent "
        "a comparison neither agent made."),
    overrides=[
        {"matcher": {"id": "byName", "options": "Tool calls"},
         "properties": [{"id": "custom.axisPlacement", "value": "right"},
                        {"id": "color",
                         "value": {"mode": "fixed", "fixedColor": "orange"}}]},
        {"matcher": {"id": "byName", "options": "Sessions"},
         "properties": [{"id": "color",
                         "value": {"mode": "fixed", "fixedColor": "blue"}}]},
    ],
    options={"orientation": "horizontal",
             "xTickLabelRotation": 0,
             "groupWidth": 0.7, "barWidth": 0.8,
             "showValue": "always",
             "legend": {"displayMode": "list", "placement": "bottom",
                        "showLegend": True},
             "tooltip": {"mode": "multi"}}))
y += 8

panels.append(panel(
    131, "Longest sessions", "table", 0, y, 24, 6,
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

y += 6

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
                "properties": [{"id": "unit", "value": TOKEN_UNIT},
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
