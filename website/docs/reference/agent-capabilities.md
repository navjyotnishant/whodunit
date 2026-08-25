---
id: agent-capabilities
title: Agent capabilities
sidebar_label: Agent capabilities
---

# Agent capabilities

**Read this before you conclude a dashboard panel is broken.**

Supported agents do not record the same things. A panel that is empty for
one agent and full for another is usually reporting that difference
correctly, not failing.

Three agents write a local transcript, and whodunit reads it: **Claude
Code**, **Codex CLI** and **Antigravity CLI**. Two more declare themselves
in the commit message and write no transcript at all — **GitHub Copilot**
and **Cursor** — and those are covered further down, along with what a
declaration cannot tell you.

Every row below was verified by reading real transcripts on a real machine
— 1,862 Claude Code transcripts, 125 Codex rollouts, 8 Antigravity
databases — rather than from vendor documentation, which describes intent
rather than what lands on disk.

## What each agent reports

The three that write a transcript. Copilot and Cursor are declaration-only
and appear in [their own section](#agents-that-declare-themselves).

| | Claude Code | Codex CLI | Antigravity (`agy`) |
|---|:---:|:---:|:---:|
| **Confidence ceiling** | `intersected` | `intersected` | `intersected` |
| Which files were edited | ✅ | ✅ | ✅ |
| The exact lines produced | ✅ | ✅ | ✅ |
| Model | ✅ | ✅ | ✅ |
| Branch | ✅ | ✅ | ❌ *none recorded* |
| MCP server | ✅ | ✅ | ✅ |
| Accept / reject outcome | ✅ | ✅ | ✅ |
| **Human edited the output** | ✅ | ❌ | ❌ |
| Input / output tokens | ✅ | ✅ | ❌ *none at all* |
| Cache reads | ✅ | ✅ | ❌ |
| Cache **writes** | ✅ | ❌ | ❌ |
| Reasoning tokens | ❌ | ✅ | ❌ |
| Per-turn timing | ❌ | ✅ | ❌ |
| Reasoning effort | ✅ | ✅ | partial |
| Permission mode | ✅ | ✅ | partial |
| Context compaction | ✅ | ✅ | ❌ |

## What this means in practice

**Antigravity contributes no cost data.** Not "reports zero" — records
none. Every token and timing column is `NULL` for an agy session, and
panels built on them exclude it from the denominator rather than counting
it as free. An agy session showing 0 tokens would make Antigravity look
like the cheapest agent available, which is the opposite of a measurement.

**Only Claude Code reports whether a human edited the output.** This is the
most interesting signal in the set — the difference between "the agent
wrote this" and "the agent wrote this and it was kept" — and it exists for
one agent out of three. The panel's denominator counts only calls that
carried the signal, so the other two do not dilute the rate.

**Only Codex reports timing.** Claude Code and Antigravity record none, so
a latency panel is structurally empty for them. It says so rather than
rendering zero, which would make them appear instantaneous.

**Codex reports cache reads but never writes.** That matters more than it
looks: the cache write payback figure is uncomputable for Codex, and a
missing denominator is not a payback of zero. See
[What the numbers mean](what-the-numbers-mean).

**Antigravity records no branch.** Its store does contain branch-shaped
strings, but they are a tracker's *suggested* branch name inside an MCP
response — not the checked-out branch. Verified absent rather than merely
unread, which is why the column is `NULL` instead of being filled from
something that looks close enough.

## Agents that declare themselves

Some agents write their own trailer into the commit and leave no local
transcript. whodunit reads those, and grades them at the weakest rung the
spec has.

| Agent | Signal | Ceiling |
|---|---|---|
| GitHub Copilot | `Agent-Logs-Url`, `Co-authored-by: Copilot` | `declared` |
| Cursor | `Made-with: Cursor` | `declared` |

The ceiling is the strongest state that agent can ever reach. A transcript
agent can reach `intersected`; a declaring agent cannot, because there is no
transcript to intersect against. The full ladder, and the four reasons a
commit carries no state at all, are in
[What the numbers mean](what-the-numbers-mean#six-states-and-only-one-of-them-is-a-problem).

**What a declared-only agent cannot report**, and why an empty panel is not
a zero:

| | Copilot | Cursor |
|---|:---:|:---:|
| That an agent was involved | ✅ | ✅ |
| Which files were edited | ❌ | ❌ |
| The exact lines produced | ❌ | ❌ |
| `ratio` — the agent's share of the commit | ❌ | ❌ |
| Tokens, cost, session shape | ❌ | ❌ |
| Model | ❌ | ❌ |

None of that is missing data waiting to be collected. A trailer states that
an agent took part; it says nothing about which lines, so every panel built
on line counts or token use is **structurally empty** for these agents
rather than reporting zero. A ratio of `0.00` would assert the agent
contributed nothing, which is the opposite of what a declaration means.

### Why `declared` and not something stronger

A trailer is the author's own assertion about their own commit, verified by
nothing. That is the definition of `declared` — "weakest: the author
declared it, nothing verified it".

The distinction is not academic. In April 2026 VS Code began adding
`Co-authored-by: Copilot` by default, and reverted it a month later after
the line appeared on commits made with Copilot switched off. A self-applied
label is evidence, and it is the least of it.

Some tools treat the same class of signal as *high* confidence. whodunit
grades it lowest, and the [method mix](what-the-numbers-mean) exists so a
reader never has to take a `declared` figure for a measured one.

**A transcript always wins.** If an agent that writes a transcript also
declares itself, the transcript's `observed` or `intersected` finding is
what gets stamped — the trailer is a fallback, not a shortcut.

### What is read

Only the commit message and its author fields. Not the diff, not the files,
not anything the agent said. A declaration is a line of metadata, and the
reader is never given anything else.

## Agents not supported

| Agent | Status | Why |
|---|---|---|
| Cursor | Partial | `declared` via its trailer; line-level attribution needs its local store — see below |
| Windsurf | Blocked | Chat history in an undocumented store with no compatibility contract |
| Antigravity IDE | Partial | Message bodies are encrypted; the CLI (`agy`) is supported |
| Gemini CLI | Blocked | Free tier removed 2026-06-18; no account to verify against |

**Cursor is a partial case rather than a blocked one.** Its trailer is read,
so its commits are attributed at `declared`. Reaching `intersected` would
need its local session store, and a 2026-08-12 survey found that store
readable rather than opaque — unencrypted session blobs, and a separate
database in which Cursor computes its *own* commit-level AI percentage.

What blocks the step up is narrower than the store: no file-edit record was
found in any of 111 readable session databases, so the shape of a Cursor
edit is unverified. Building a parser against a format nobody has observed
would be inventing one, which is the same reason Gemini CLI is parked.

The adapter interface is open for a contribution.

## The measured detail

This page is the version you read before installing. The full inventory —
every field, with occurrence counts and a privacy classification — is in
[`docs/adapters/field-inventory.md`](https://github.com/navjyotnishant/whodunit/blob/main/docs/adapters/field-inventory.md)
and the per-agent store formats are in
[`docs/adapters/agent-support.md`](https://github.com/navjyotnishant/whodunit/blob/main/docs/adapters/agent-support.md).
