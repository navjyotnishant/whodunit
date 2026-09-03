#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-03
# Last updated: 2026-09-03
# Description: Fetch per-model API list prices from the providers' pricing
# pages into whodunit_model_prices, so the Investment Case dashboard prices
# each model at its own rate rather than one typed number for all.
"""Fetch model list prices into the lake.

One price per million tokens is wrong the moment two models are in use:
Opus is five times Sonnet. The Investment Case dashboard used to take a
single typed price; it now joins whodunit_model_prices, which this script
fills from the two public pricing pages.

Fetched, not typed — but fail-closed. A pricing page that changes shape
parses to nothing, and nothing must not become zero. If a provider yields
fewer rows than the baked table below, the baked rows are written for that
provider with source 'baked <date>' so the dashboard can show that the
prices are a snapshot rather than a fetch. Either way fetched_at is stored
and shown, so a reader knows how old the prices are.

Model keys match what agents record in whodunit_sessions.model:
Anthropic 'Claude Opus 4.8' becomes claude-opus-4-8; OpenAI ids are used
as printed. A session whose model has no row is reported as unpriced, not
priced at zero.

Usage:
    python3 fetch-model-prices.py                       # lake only
    python3 fetch-model-prices.py --database lake --database lake_demo
    python3 fetch-model-prices.py --offline             # baked rows only
"""

import argparse
import datetime as dt
import html
import re
import subprocess
import sys
import urllib.request

ANTHROPIC_URL = "https://platform.claude.com/docs/en/about-claude/pricing"
OPENAI_URL = "https://developers.openai.com/api/docs/pricing"
UA = "whodunit-price-fetch/1 (+https://github.com/navjyotnishant/whodunit)"

# Snapshot of both pages on 2026-09-03. Used when a fetch fails or parses
# thin, and written with source 'baked 2026-09-03' so the dashboard says so.
BAKED_DATE = "2026-09-03"
BAKED = {
    # model, provider, input, cache_read, cache_write(5m), output
    "anthropic": [
        ("claude-fable-5-1", 10, 0.25, 12.5, 50),
        ("claude-mythos-5-1", 10, 0.25, 12.5, 50),
        ("claude-fable-5", 10, 1, 12.5, 50),
        ("claude-mythos-5", 10, 1, 12.5, 50),
        ("claude-opus-5", 5, 0.5, 6.25, 25),
        ("claude-opus-4-8", 5, 0.5, 6.25, 25),
        ("claude-opus-4-7", 5, 0.5, 6.25, 25),
        ("claude-opus-4-6", 5, 0.5, 6.25, 25),
        ("claude-opus-4-5", 5, 0.5, 6.25, 25),
        ("claude-opus-4-1", 15, 1.5, 18.75, 75),
        ("claude-opus-4", 15, 1.5, 18.75, 75),
        ("claude-sonnet-5", 2, 0.2, 2.5, 10),
        ("claude-sonnet-4-6", 3, 0.3, 3.75, 15),
        ("claude-sonnet-4-5", 3, 0.3, 3.75, 15),
        ("claude-sonnet-4", 3, 0.3, 3.75, 15),
        ("claude-haiku-4-5", 1, 0.1, 1.25, 5),
        ("claude-haiku-3-5", 0.8, 0.08, 1, 4),
    ],
    # OpenAI bills cached input at a discount and nothing extra to write
    # the cache, so cache_write is the input price.
    "openai": [
        ("gpt-5", 1.25, 0.125, 1.25, 10),
        ("gpt-5-mini", 0.25, 0.025, 0.25, 2),
        ("gpt-5-nano", 0.05, 0.005, 0.05, 0.4),
        ("gpt-5.1", 1.25, 0.125, 1.25, 10),
        ("gpt-5.2", 1.75, 0.175, 1.75, 14),
        ("gpt-5.3-codex", 1.75, 0.175, 1.75, 14),
        ("gpt-5.4", 2.5, 0.25, 2.5, 15),
        ("gpt-5.4-mini", 0.75, 0.075, 0.75, 4.5),
        ("gpt-5.4-nano", 0.2, 0.02, 0.2, 1.25),
        ("gpt-5.5", 5, 0.5, 5, 30),
        ("gpt-5.6-terra", 2, 0.2, 2, 12),
        ("gpt-5.6-sol", 4, 0.4, 4, 20),
        ("gpt-5.6-luna", 0.2, 0.02, 0.2, 1.2),
    ],
}

DDL = """CREATE TABLE IF NOT EXISTS whodunit_model_prices (
  model           VARCHAR(64)   NOT NULL,
  provider        VARCHAR(16)   NOT NULL,
  input_usd       DECIMAL(10,4) NOT NULL,
  cache_read_usd  DECIMAL(10,4) NOT NULL,
  cache_write_usd DECIMAL(10,4) NOT NULL,
  output_usd      DECIMAL(10,4) NOT NULL,
  source          VARCHAR(255)  NOT NULL,
  fetched_at      DATETIME      NOT NULL,
  PRIMARY KEY (model)
)"""


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": "text/html"})
    with urllib.request.urlopen(req, timeout=20) as r:
        return r.read().decode("utf-8", "replace")


def strip_tags(page):
    page = re.sub(r"<script.*?</script>|<style.*?</style>", " ", page, flags=re.S)
    text = re.sub(r"<[^>]+>", " ", page)
    return re.sub(r"\s+", " ", html.unescape(text))


# A main-table row, tags stripped, reads
#   "Claude Opus 4.8 $5 / MTok $6.25 / MTok $10 / MTok $0.50 / MTok $25 / MTok"
# in the order input, 5m write, 1h write, cache read, output; a footnote
# marker can follow an amount as "MTok 1". The batch
# table later on the page has two amounts per row and cannot match five in
# a row, so the first match per model is the main table.
ANTHROPIC_ROW = re.compile(
    r"Claude (Fable|Mythos|Opus|Sonnet|Haiku) (\d+(?:\.\d+)?)[^$]{0,200}?"
    r"\$([\d.]+) / MTok(?:\s*\d)?\s*\$([\d.]+) / MTok(?:\s*\d)?\s*\$([\d.]+) / MTok(?:\s*\d)?\s*"
    r"\$([\d.]+) / MTok(?:\s*\d)?\s*\$([\d.]+) / MTok")


def parse_anthropic(text):
    rows = {}
    for fam, ver, inp, w5, _w1, read, out in ANTHROPIC_ROW.findall(text):
        key = f"claude-{fam.lower()}-{ver.replace('.', '-')}"
        rows.setdefault(key, (key, float(inp), float(read), float(w5), float(out)))
    return list(rows.values())


# "gpt-5.3-codex $1.75 $0.175 $14.00"; pro rows have a dash for cached.
# The OpenAI table is rendered by JavaScript and only a few rows reach the
# static HTML (5 of 13 on 2026-09-03), so this parse is expected to come in
# thin and the baked rows to be used until the page changes. Kept so a
# server-rendered page starts winning without a code change.
OPENAI_ROW = re.compile(
    r"\b(gpt-5(?:\.\d+)?(?:-[a-z]+)?)\s*\$([\d.]+)\s*(?:\$([\d.]+)|[—–-])\s*\$([\d.]+)")


def parse_openai(text):
    rows = {}
    for model, inp, cached, out in OPENAI_ROW.findall(text):
        if model.endswith("-pro"):
            continue  # no cached price listed; not a coding-agent model
        cr = float(cached) if cached else float(inp)
        rows.setdefault(model, (model, float(inp), cr, float(inp), float(out)))
    return list(rows.values())


def mysql(sql, container, database):
    r = subprocess.run(
        ["docker", "exec", "-i", container, "mysql", "-uroot", "-padmin", database],
        input=sql, capture_output=True, text=True)
    if r.returncode:
        raise SystemExit(f"mysql failed: {r.stderr.strip()[-300:]}")
    return r.stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--container", default="devlake-mysql-1")
    ap.add_argument("--database", action="append", default=[])
    ap.add_argument("--offline", action="store_true", help="write the baked table only")
    args = ap.parse_args()
    databases = args.database or ["lake"]
    now = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M:%S")

    rows = []
    for provider, url, parse in (("anthropic", ANTHROPIC_URL, parse_anthropic),
                                 ("openai", OPENAI_URL, parse_openai)):
        parsed, source = [], f"baked {BAKED_DATE}"
        if not args.offline:
            try:
                parsed = parse(strip_tags(fetch(url)))
            except Exception as e:  # network or shape — fall back, say so
                print(f"{provider}: fetch failed ({e.__class__.__name__}), using baked rows",
                      file=sys.stderr)
        # Fail closed: a page that parses thinner than the snapshot is a
        # page that changed shape, not a provider that dropped models.
        if len(parsed) >= len(BAKED[provider]):
            source = url
        else:
            if parsed:
                print(f"{provider}: parsed {len(parsed)} rows, baked has "
                      f"{len(BAKED[provider])} — using baked", file=sys.stderr)
            parsed = BAKED[provider]
        rows += [(m, provider, i, r, w, o, source) for m, i, r, w, o in parsed]

    values = ",\n".join(
        f"('{m}','{p}',{i},{r},{w},{o},'{s}','{now}')" for m, p, i, r, w, o, s in rows)
    sql = DDL + ";\n" + f"""
INSERT INTO whodunit_model_prices
  (model, provider, input_usd, cache_read_usd, cache_write_usd, output_usd, source, fetched_at)
VALUES
{values}
ON DUPLICATE KEY UPDATE
  provider=VALUES(provider), input_usd=VALUES(input_usd), cache_read_usd=VALUES(cache_read_usd),
  cache_write_usd=VALUES(cache_write_usd), output_usd=VALUES(output_usd),
  source=VALUES(source), fetched_at=VALUES(fetched_at);
"""
    for db in databases:
        mysql(sql, args.container, db)
        by = {}
        for _, p, *_r, s in rows:
            by.setdefault(p, s)
        print(f"{db}: {len(rows)} model prices written — " +
              ", ".join(f"{p}: {'fetched' if s.startswith('http') else s}" for p, s in by.items()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
