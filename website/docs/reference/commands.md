---
id: commands
title: Command reference
sidebar_label: Command reference
---

# Command reference

Every command's own `--help` is authoritative; this is the map.

## Setting up

| Command | What it does |
|---|---|
| `dun init [--repo <path>]` | Install the git hooks into a repository and register it |
| `dun verify` | Check the install end to end and name what to fix |
| `dun config` | Show every setting and whether it was set or defaulted |
| `dun config set <key> <value>` | Change one setting |
| `dun config datalake` | Configure the sync target interactively |

## Day to day

| Command | What it does |
|---|---|
| `dun status` | Trailer coverage and method mix; outside a repository, every instrumented one |
| `dun report` | Self-contained local HTML report — no server, no network |
| `dun log` | What the hooks did, including every error they swallowed |
| `dun repos list` | What you have instrumented |
| `dun repos candidates` | Repositories with agent activity and no hooks |
| `dun repos update` | Reinstall hooks everywhere after an upgrade |
| `dun repos remove` | Stop tracking a repository |

## Measuring

| Command | What it does |
|---|---|
| `dun baseline capture` | Snapshot delivery metrics over a named pre-adoption window |
| `dun delta` | Compare delivery before and after adoption |
| `dun ingest [--since <time>]` | Read agent transcripts into the journal |
| `dun daemon run` | Foreground watcher; re-ingests as sessions change |

## Publishing

| Command | What it does |
|---|---|
| `dun sync` | Publish to the configured database |
| `dun sync --dry-run` | Print the exact payload without contacting anything |
| `dun journal show` | Print everything recorded locally |
| `dun journal purge` | Delete it, for the current repository only |

## CI

| Command | What it does |
|---|---|
| `dun check --base <ref>` | Fail if any commit since `<ref>` lacks a valid trailer |

## Maintenance

| Command | What it does |
|---|---|
| `dun update` | Upgrade through Homebrew, then refresh every repository's hooks |
| `dun version` | The installed version |

## Capture a baseline

If you ever want to answer "did this change how we ship?", capture the
period you were working without an agent:

```sh
dun baseline capture --since 2026-01-01 --until 2026-06-30
```

It records commit volume, median diff size, revert rate, cadence and
purpose distribution over that window as a dated, immutable snapshot.

**Name the window explicitly.** A bare `dun baseline capture` prints help
rather than capturing: a window ending today stops being pre-adoption the
moment hooks are installed, so the convenient default would measure
AI-assisted work and record it as the *before*. `--days 90` still does
that, and is correct only before `dun init`.

**It does not have to run before `dun init`.** The window is one you name,
and it is read from history git already has — so a baseline can be captured
afterwards, as long as the range you pick ended before your first assisted
commit. What closes permanently is the availability of an untouched window,
not the opportunity to capture one.

Baselines live outside `data/` because a snapshot is not rebuildable:
everything else in the journal can be recovered by re-ingesting transcripts,
and this cannot. Recapturing over the wrong window needs `--force`, which
prints the capture date, window and commit count of the snapshot it is about
to destroy, and stores the new one as a separate baseline rather than
overwriting the old row.

PR throughput, cycle time and change-failure rate cannot be read from git,
so they are optional flags you supply from your own dashboard
(`--prs-merged`, `--median-cycle-hours`, `--change-failure-rate`). Anything
not passed is omitted from the snapshot rather than recorded as zero.
