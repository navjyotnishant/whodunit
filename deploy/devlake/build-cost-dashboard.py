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


# Colour is set per panel rather than left to Grafana's default.
#
# The default is `palette-classic`, the saturated green/yellow/red set
# Grafana has carried since v6. It looks dated, and worse, it assigns
# meaning where there is none: a model rendered red is not a warning, but
# it reads as one beside panels where red genuinely means "below
# break-even".
#
# So: continuous single-hue ramps where the value IS the message (bar
# gauges, bar charts — bigger is just bigger), a modern categorical
# palette where series are distinct things (the per-model time series),
# and explicit thresholds only where a colour is a judgement.
# Bar gauges auto-scale their value text to fill the panel, so a panel
# with few bars renders each number enormous — five bars in a seven-unit
# panel gave "3,383" the height of a section heading. The cap keeps the
# value readable as a label rather than as the headline; the bar length is
# what carries the comparison.
BARGAUGE = {"displayMode": "gradient", "orientation": "horizontal",
            "showUnfilled": True, "valueMode": "text",
            "text": {"valueSize": 18, "titleSize": 12},
            "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}

# Red / amber / green, for the panels where a value genuinely is a
# problem, a concern, or fine.
#
# Applied ONLY to those. A magnitude coloured red — a large token count, a
# long session — reads as a warning it is not, and once every panel is
# traffic-lit the two that mean it stop standing out. Amber exists so
# "watch this" is sayable: a two-step scale forces every value into good
# or bad, and most of the interesting ones sit between.
def traffic(bad_below=None, ok_above=None, *, invert=False):
    """Three bands. Values below `bad_below` red, above `ok_above` green.

    invert=True flips it for metrics where lower is better.
    """
    if invert:
        return [{"color": "green", "value": None},
                {"color": "orange", "value": bad_below},
                {"color": "red", "value": ok_above}]
    return [{"color": "red", "value": None},
            {"color": "orange", "value": bad_below},
            {"color": "green", "value": ok_above}]


CONTINUOUS_BLUES = {"mode": "continuous-BlPu"}
CONTINUOUS_GREENS = {"mode": "continuous-GrYlRd"}
CATEGORICAL = {"mode": "palette-classic-by-name"}
NEUTRAL = {"mode": "fixed", "fixedColor": "text"}


def panel(pid, title, ptype, x, y, w, h, sql, *, unit=None, decimals=None,
          novalue=None, description=None, options=None, overrides=None,
          thresholds=None, minval=None, fmt="table", color=None):
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
    if color:
        d["color"] = color
    if thresholds:
        d["thresholds"] = {"mode": "absolute", "steps": thresholds}
    elif color and color is not NEUTRAL:
        # A palette only takes effect when nothing overrides it, and
        # Grafana injects a default green/red threshold set on stats and
        # gauges when none is given. Cleared explicitly.
        d["thresholds"] = {"mode": "absolute",
                           "steps": [{"color": "text", "value": None}]}
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
    color=NEUTRAL,
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
    color=NEUTRAL,
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
    color=NEUTRAL,
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
    color=NEUTRAL,
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
    thresholds=traffic(bad_below=50, ok_above=75),
    options=STAT))

panels.append(panel(
    103, "Cache write payback", "stat", 20, y, 4, 4,
    f"""SELECT ROUND(SUM(COALESCE(s.cache_read_tokens,0))
  / NULLIF(SUM(s.cache_write_tokens), 0), 2) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.cache_write_tokens IS NOT NULL AND s.cache_write_tokens > 0 AND {CONTRIBUTOR}""",
    unit="suffix: ×", decimals=1, novalue="no agent reported cache writes",
    description=(
        f"Reads returned per token written to cache — a **ratio, not a "
        f"percentage**. 135 means each cached token was read back 135 "
        f"times.\n\n"
        f"Below {BREAK_EVEN}x the "
        "writes cost more than they saved — a write is billed at about 1.25x "
        "base and a read at 0.1x.\n\n"
        "**The obvious break-even of 1.0 is wrong**: a series at 1.10x lost "
        "money while looking healthy.\n\n"
        "Empty for Codex, which reports cache reads but never writes."),
    thresholds=traffic(bad_below=BREAK_EVEN, ok_above=3),
    options=STAT))

panels.append(panel(
    105, "Reasoning tokens", "stat", 18, y + 9, 6, 4,
    f"""SELECT COALESCE(SUM(s.reasoning_tokens), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.reasoning_tokens IS NOT NULL AND {CONTRIBUTOR}""",
    unit=TOKEN_UNIT, decimals=1, novalue="only Codex separates these",
    description=(
        "Tokens spent thinking rather than answering.\n\n"
        "Codex alone reports this. Claude Code and Antigravity do not "
        "separate it, so an empty panel here means 'not reported', not zero."),
    color=NEUTRAL,
    options=STAT))

# The three signals below sit on the headline's second row rather than in
# a section of their own further down.
#
# They answer "what did this cost us" as directly as the token counts do —
# effort is the setting that drives token spend, latency is the wait it
# buys, and the human-edited share is whether the output survived contact
# with a reviewer. A reader who only looks at the top of the page should
# see them.
# Its own row rather than a share of the headline. This is the only
# section that recommends an action — everything above reports what
# happened — and it was previously four tiles squeezed onto the end of the
# token row, with the compact rate the narrowest of them.
panels.append(row("Context pressure — the part you can act on", y + 4))
SHAPE_Y = y + 5
# 4 for the token tiles, 1 for the row header, 8 for two panel rows.
y += 13

panels.append(panel(
    130, "Human edited the agent's output", "stat", 6, SHAPE_Y + 4, 6, 4,
    f"""SELECT ROUND(100.0 * SUM(CASE WHEN e.user_modified = 1 THEN 1 ELSE 0 END)
  / NULLIF(COUNT(*), 0), 1) AS v
FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
WHERE e.user_modified IS NOT NULL AND {CONTRIBUTOR}""",
    unit="percent", decimals=1, novalue="only Claude Code reports this",
    thresholds=traffic(bad_below=20, ok_above=40, invert=True),
    description=(
        "Share of edits a human changed before committing — the difference "
        "between 'the agent wrote this' and 'the agent wrote this and it was "
        "kept'.\n\n"
        "Claude Code alone reports it. The denominator counts only calls that "
        "carried the signal, so agents without it do not dilute the rate."),
    options=STAT))

panels.append(panel(
    132, "Turn latency", "stat", 0, SHAPE_Y + 4, 6, 4,
    f"""SELECT ROUND(AVG(s.duration_ms) / 1000, 1) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.duration_ms IS NOT NULL AND {CONTRIBUTOR}""",
    unit="s", decimals=1, novalue="only Codex records timing",
    color=NEUTRAL,
    description=(
        "Codex alone records per-turn timing. Claude Code and Antigravity "
        "report none, so this is empty for them rather than zero — a zero "
        "would make them the fastest agents on this panel."),
    options=STAT))

panels.append(panel(
    134, "Sessions that compacted", "stat", 0, SHAPE_Y, 6, 4,
    f"""SELECT ROUND(100.0 * SUM(CASE WHEN s.compactions > 0 THEN 1 ELSE 0 END)
  / NULLIF(COUNT(*), 0), 0) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.compactions IS NOT NULL AND {CONTRIBUTOR}""",
    unit="percent", decimals=0, novalue="no agent reported compaction",
    description=(
        "Share of sessions whose context was compacted at least once "
        "(NAV-106).\n\n"
        "**Read this beside the context the sessions were running at.** A "
        "long session costs more even when cached, because the whole context "
        "is re-sent every turn — and compacting is the one thing a person "
        "can do about it. Measured on this machine, 92% of turns ran above "
        "150k context while a small fraction of sessions ever compacted.\n\n"
        "**Deliberately not colour-coded.** A low rate is only a problem "
        "alongside high context, and this panel cannot see context — a short "
        "session has nothing to compact, and colouring 8% red would call "
        "that a failure. The number is the finding; the judgement needs the "
        "context distribution, which the journal cannot answer per turn "
        "yet.\n\n"
        "The denominator counts only sessions from agents that report the "
        "signal at all. Antigravity has no equivalent and is excluded rather "
        "than counted as never compacting."),
    color=NEUTRAL,
    options=STAT))

panels.append(panel(
    135, "Compact rate by agent", "bargauge", 6, SHAPE_Y, 12, 4,
    f"""SELECT
  s.agent AS metric,
  ROUND(100.0 * SUM(CASE WHEN s.compactions > 0 THEN 1 ELSE 0 END)
    / NULLIF(COUNT(*), 0)) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.compactions IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.agent ORDER BY 2 DESC""",
    unit="percent", decimals=0, novalue="no agent reported compaction",
    color=CONTINUOUS_GREENS,
    description=(
        "The same rate split by agent — the cut that says whether this is a "
        "habit or a tool difference.\n\n"
        "Measured here: Claude Code 15% against Codex 4%, on the same "
        "repositories and the same person. A gap that size is more likely "
        "about how each agent surfaces the option than about who was "
        "using it.\n\n"
        "Antigravity is absent rather than shown at 0%: it records no "
        "context management, so it cannot report a rate at all (NAV-21)."),
    options=BARGAUGE))

panels.append(panel(
    136, "Compactions per compacting session", "stat", 18, SHAPE_Y, 6, 4,
    f"""SELECT ROUND(AVG(s.compactions), 1) AS v
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.compactions > 0 AND {CONTRIBUTOR}""",
    decimals=1, novalue="no session has compacted",
    color=NEUTRAL,
    description=(
        "Average compactions, counting only sessions that compacted at "
        "all.\n\n"
        "The zeroes are excluded deliberately. Averaged over every session "
        "this would be a fraction and would say nothing; the question is "
        "what a compacting session looks like — measured here, 4 on "
        "average, with one session reaching 9.\n\n"
        "A high number is not automatically bad: it means long work, which "
        "is sometimes exactly right."),
    options=STAT))

panels.append(panel(
    133, "Reasoning effort", "piechart", 12, SHAPE_Y + 4, 6, 4,
    f"""SELECT s.effort AS metric, COUNT(*) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.effort IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.effort""",
    novalue="no session recorded an effort tier",
    # Explicit colours rather than a palette. Effort is an ORDERED scale —
    # low through xhigh — and a palette assigns hues by hashing the series
    # name, which put "medium" and "high" in the same red region and lost
    # the ordering entirely. Cool to warm makes the tier legible without
    # reading the legend.
    color=NEUTRAL,
    overrides=[
        {"matcher": {"id": "byName", "options": tier},
         "properties": [{"id": "color",
                         "value": {"mode": "fixed", "fixedColor": hue}}]}
        for tier, hue in (("low", "#5794F2"),      # blue
                          ("medium", "#73BF69"),   # green
                          ("high", "#FF9830"),     # orange
                          ("xhigh", "#F2495C"))    # red — the top of the scale
    ],
    description=(
        "How hard the model was asked to think, where the agent says.\n\n"
        "The lever behind the token counts above: a higher tier spends more "
        "on the same task."),
    options={"legend": {"displayMode": "list", "placement": "bottom"},
             "reduceOptions": {"calcs": ["lastNotNull"], "values": True}}))

# ------------------------------------------------------------- per model
panels.append(row("Per model — where the aggregate hides a loss", y)); y += 1

panels.append(panel(
    109, "Token mix by model", "barchart", 0, y, 12, 6,
    f"""SELECT
  COALESCE(NULLIF(s.model, ''), '(unattributed)') AS model,
  SUM(s.input_tokens)                             AS `Uncached in`,
  SUM(s.output_tokens)                            AS `Output`,
  SUM(COALESCE(s.cache_write_tokens,0))           AS `Cache write`,
  SUM(COALESCE(s.cache_read_tokens,0))            AS `Cache read`
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.input_tokens IS NOT NULL AND {CONTRIBUTOR}
GROUP BY 1
ORDER BY SUM(s.input_tokens + s.output_tokens
           + COALESCE(s.cache_read_tokens,0)
           + COALESCE(s.cache_write_tokens,0)) DESC""",
    unit="percentunit", decimals=0,
    novalue="no session reported tokens",
    color=CATEGORICAL,
    description=(
        "What each model's tokens are made of — one stacked bar per model.\n\n"
        "**Stacked as a percentage, not as absolute counts, and that is the "
        "only way this reads.** Cache reads are 88-99% of every model's "
        "total here, so an absolute stack would draw one long bar with three "
        "invisible slivers: opus-5's entire output is 0.1% of its own total. "
        "Normalising each bar to 100% makes the composition comparable, "
        "which is where the difference actually is — gpt-5.4 is 94% cache "
        "reads against gpt-5.5 at 88%.\n\n"
        "Absolute totals are in the table below; this panel answers what the "
        "tokens were, not how many.\n\n"
        "Ordered by total tokens, so the largest consumer is at the top even "
        "though every bar is the same length."),
    overrides=[
        {"matcher": {"id": "byName", "options": name},
         "properties": [{"id": "color",
                         "value": {"mode": "fixed", "fixedColor": hue}}]}
        for name, hue in (("Cache read", "#5794F2"),    # blue: the cheap bulk
                          ("Cache write", "#B877D9"),   # purple: the 1.25x premium
                          ("Uncached in", "#FF9830"),   # orange: full price
                          ("Output", "#73BF69"))        # green: what was produced
    ],
    options={"orientation": "horizontal",
             "stacking": "percent",
             "xTickLabelRotation": 0,
             "showValue": "never",
             "legend": {"displayMode": "list", "placement": "bottom",
                        "showLegend": True},
             "tooltip": {"mode": "multi", "sort": "desc"}}))

panels.append(panel(
    131, "Longest sessions", "bargauge", 12, y, 12, 6,
    f"""SELECT
  CONCAT(
    ROW_NUMBER() OVER (ORDER BY (s.last_seen - s.first_seen) DESC),
    '. ', s.agent, ' · ',
    COALESCE(NULLIF(s.model, ''), 'model not reported')) AS metric,
  ROUND((s.last_seen - s.first_seen) / 1000000000 / 3600, 1) AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE {CONTRIBUTOR}
  AND s.last_seen > s.first_seen
  -- A session whose first record predates the ingest window carries the
  -- zero time, which reaches the database as -6795364578871345152 and
  -- renders as a 272-year session. The adapter no longer writes those,
  -- but a database synced before that fix still holds them, and one such
  -- row at the top of a DESC sort makes the whole panel nonsense.
  AND s.first_seen > 0
ORDER BY (s.last_seen - s.first_seen) DESC LIMIT 8""",
    unit="h", decimals=1, novalue="no sessions with a usable duration",
    color=CONTINUOUS_GREENS,
    description=(
        "The eight longest sessions, in hours. A long session costs more even "
        "when cached, because the whole context is re-sent every turn.\n\n"
        "Individual sessions rather than an average, deliberately: the mean "
        "here is a few hours and the top session is 339, so an average would "
        "hide exactly the ones worth looking at.\n\n"
        "**Rows are numbered because the labels repeat.** Several of the "
        "longest sessions are the same agent with no model recorded, and a "
        "bar gauge needs distinct labels or it silently collapses them into "
        "one bar.\n\n"
        "Sessions whose start time was never recorded are excluded rather "
        "than rendered as impossibly long."),
    options=BARGAUGE))

panels.append(panel(
    110, "Token use and cache efficiency by model", "table", 0, y + 6, 24, 9,
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
    / NULLIF(SUM(s.cache_write_tokens), 0), 1)    AS `Write payback ×`
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
        {"matcher": {"id": "byName", "options": "Write payback ×"},
         "properties": [
             {"id": "custom.cellOptions",
              "value": {"type": "color-text"}},
             {"id": "thresholds",
              "value": {"mode": "absolute",
                        "steps": traffic(bad_below=BREAK_EVEN, ok_above=3)}},
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
# 6 for the two gauges above, 9 for the table itself.
y += 15

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
    fmt="time_series", color=CATEGORICAL,
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
panels.append(row("Where it landed", y)); y += 1

panels.append(panel(
    120, "Top agent-written lines by branch", "barchart", 0, y, 24, 8,
    # Twelve, not the twenty-five the table showed.
    #
    # Branch names here run to 76 characters and there are 42 of them, most
    # contributing under 400 lines. A bar per branch is unreadable; a bar
    # per branch that mostly renders as a hairline is unreadable AND
    # pointless. The tail is summarised by the panel beside this one rather
    # than truncated silently, which is what the old LIMIT 25 did.
    # The twelve named branches, then everything else as one bar.
    #
    # That last bar is what replaced a separate "42 branches touched"
    # stat: it answers the same question — is this the whole picture? —
    # inside the chart, at the scale of the thing it is being compared
    # against. Measured here the top twelve are 90% of all lines written,
    # so the tail is visibly small rather than merely absent.
    f"""SELECT branch, `Lines added` FROM (
  SELECT
    CASE WHEN CHAR_LENGTH(e.branch) > 34
         THEN CONCAT(LEFT(e.branch, 31), '...')
         ELSE e.branch END      AS branch,
    SUM(e.lines_added)          AS `Lines added`,
    1                           AS grp,
    ROW_NUMBER() OVER (ORDER BY SUM(e.lines_added) DESC) AS rn
  FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
  WHERE e.branch IS NOT NULL AND {CONTRIBUTOR}
  GROUP BY e.branch
  HAVING SUM(e.lines_added) > 0
) ranked WHERE rn <= 12
UNION ALL
SELECT
  CONCAT('+ ', COUNT(*), ' more branches'),
  SUM(lines_added)
FROM (
  SELECT
    SUM(e.lines_added) AS lines_added,
    ROW_NUMBER() OVER (ORDER BY SUM(e.lines_added) DESC) AS rn
  FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
  WHERE e.branch IS NOT NULL AND {CONTRIBUTOR}
  GROUP BY e.branch
  HAVING SUM(e.lines_added) > 0
) tail WHERE rn > 12
HAVING COUNT(*) > 0""",
    unit=TOKEN_UNIT, novalue="no event recorded a branch",
    color=CONTINUOUS_BLUES,
    description=(
        "Where the agent's output actually landed, top twelve branches by "
        "lines written, with everything below them summed into a final "
        "bar.\n\n"
        "That last bar is there so the top twelve reads as a slice rather "
        "than as everything — the failure the old table's silent LIMIT 25 "
        "already caused once. Measured here the twelve are 90% of all lines "
        "written across 42 branches.\n\n"
        "Branches contributing zero lines are excluded — those are sessions "
        "that read and ran things without writing, which the events count "
        "already covers elsewhere.\n\n"
        "Long branch names are truncated for the axis; the tooltip carries "
        "the value and the panel beside this one says how much sits outside "
        "the top twelve.\n\n"
        "Antigravity records no branch at all — its branch-like strings are a "
        "tracker's *suggested* branch inside an MCP response, not the "
        "checked-out one. Its events are absent here rather than grouped "
        "under a wrong name."),
    options={"orientation": "horizontal",
             "xTickLabelRotation": 0,
             "showValue": "always",
             "legend": {"showLegend": False},
             "tooltip": {"mode": "single"}}))


y += 8


# ------------------------------------------------------ MCP and autonomy
panels.append(row("MCP servers, and how much rope the agent had", y)); y += 1

panels.append(panel(
    121, "Autonomy granted — tool calls per session", "bargauge", 12, y, 12, 7,
    f"""SELECT
  CONCAT(s.permission_mode, '  (', s.agent, ')')  AS metric,
  ROUND(SUM(s.tool_calls) / COUNT(*))              AS value
FROM whodunit_sessions s JOIN whodunit_repos r ON r.repo_id = s.repo_id
WHERE s.permission_mode IS NOT NULL AND {CONTRIBUTOR}
GROUP BY s.permission_mode, s.agent
ORDER BY 2 DESC""",
    decimals=0, novalue="no session recorded a permission mode",
    color=CONTINUOUS_GREENS,
    description=(
        "How much work happens per session at each level of autonomy.\n\n"
        "**One metric rather than two bars.** Sessions and total tool calls "
        "run in opposite directions here — 64 sessions on Codex's `never` "
        "produced 506 calls, 2 Claude Code sessions on `auto` produced "
        "6,766 — so charting both put a 13x range beside a 32x one and "
        "flattened whichever shared the axis. Dividing them gives the thing "
        "both were circling: 8 calls per session on `never`, 3,383 on "
        "`auto`.\n\n"
        "The ladder is monotonic across every mode measured, which is the "
        "finding: autonomy is not how often it is granted, it is how much "
        "happens once it is.\n\n"
        "The agent is in the label because each mode belongs to exactly one "
        "— Codex says `never` and `on-request`, Claude Code says "
        "`acceptEdits`, `auto` and `default`. Each agent's own vocabulary is "
        "kept rather than mapped onto a shared enum: asserting that `never` "
        "means the same as `default` would invent a comparison neither agent "
        "made."),
    options=BARGAUGE))

panels.append(panel(
    140, "Calls per MCP server", "barchart", 0, y, 12, 7,
    f"""SELECT e.mcp_server AS metric, COUNT(*) AS value
FROM whodunit_events e JOIN whodunit_repos r ON r.repo_id = e.repo_id
WHERE e.mcp_server IS NOT NULL AND {CONTRIBUTOR}
GROUP BY e.mcp_server ORDER BY COUNT(*) DESC LIMIT 15""",
    novalue="no MCP calls recorded",
    color=CONTINUOUS_BLUES,
    description=(
        "Which servers the agents actually reach for.\n\n"
        "Antigravity is absent: what looked like MCP identity in its store "
        "turned out to be the command text being approved, which is content "
        "rather than an identifier."),
    options={"legend": {"showLegend": False},
             "orientation": "horizontal"}))

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
         "query": "SELECT DISTINCT COALESCE(NULLIF(contributor, ''), '(unattributed)') AS __text, COALESCE(NULLIF(contributor, ''), '') AS __value FROM whodunit_repos ORDER BY 1",
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
