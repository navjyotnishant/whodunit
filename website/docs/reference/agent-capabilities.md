---
id: agent-capabilities
title: Agent capabilities
sidebar_label: Agent capabilities
---

# Agent capabilities

**Read this before you conclude a dashboard panel is broken.**

The three supported agents do not record the same things. A panel that is
empty for one agent and full for another is usually reporting that
difference correctly, not failing.

Every row below was verified by reading real transcripts on a real machine
— 1,862 Claude Code transcripts, 125 Codex rollouts, 8 Antigravity
databases — rather than from vendor documentation, which describes intent
rather than what lands on disk.

## What each agent reports

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

## Agents not supported

| Agent | Status | Why |
|---|---|---|
| Cursor | Blocked | Chat history in an undocumented SQLite blob with no compatibility contract |
| Windsurf | Blocked | Same |
| Antigravity IDE | Partial | Message bodies are encrypted; the CLI (`agy`) is supported |
| Gemini CLI | Blocked | Free tier removed 2026-06-18; no account to verify against |

Cursor and Windsurf are declaration-only by decision, not by oversight.
Community parsers for those stores break on minor version bumps, and
reverse-engineering them is not a maintenance burden this project takes on.
The adapter interface is open for a contribution.

## The measured detail

This page is the version you read before installing. The full inventory —
every field, with occurrence counts and a privacy classification — is in
[`docs/adapters/field-inventory.md`](https://github.com/navjyotnishant/whodunit/blob/main/docs/adapters/field-inventory.md)
and the per-agent store formats are in
[`docs/adapters/agent-support.md`](https://github.com/navjyotnishant/whodunit/blob/main/docs/adapters/agent-support.md).
