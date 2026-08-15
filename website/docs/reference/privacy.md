---
id: privacy
title: Privacy
sidebar_label: Privacy
---

# Privacy

## Collection makes no network calls

The hooks, the daemon and `dun ingest` read git and local transcripts and
write a local SQLite journal. Nothing contacts the network.

The one exception is opt-out and unrelated to collection: `dun` checks once
a day whether a newer release exists, on bare `dun` only, never from a
hook. Disable it with `dun config set version_check off`, or
`DUN_NO_VERSION_CHECK` / `DO_NOT_TRACK` in the environment.

## What is never recorded

The journal schema **has no field that could hold** prompt text, message
content, file contents, hostnames or remote URLs. This is a structural
guarantee rather than a filtering step — there is nowhere to put them.

Explicitly out of scope, and named because they were considered and
rejected: Claude Code's `toolUseResult` as a whole (it carries stdout,
stderr, and the before/after text of every edit), Codex's
`agent_message.message`, `agent_reasoning.text`, `user_instructions` and
`unified_diff`, and Antigravity's `CodeContent` and `ReplacementContent`.

Two more are countable but never stored, because they carry user intent
verbatim: MCP tool-call arguments, and web-search queries.

## What *is* recorded, and identifies someone

Stated plainly, because a privacy page that omits this is worse than none —
a reader who finds it later has reason to distrust everything else on the
page.

**File paths.** The journal stores which files an agent edited. That
reveals what someone worked on, not merely how much.

**A contributor email.** `whodunit_repos` stores the git committer email
for the repository — the same address already in every commit you author.
A shared database needs it to attribute anything to anyone.

Both stay local until you run `dun sync`.

## Where it lives

| Path | What |
|---|---|
| `~/.whodunit/config.json` | Settings: retention, backups, sync target, version check |
| `~/.whodunit/repos.json` | Which repositories you instrumented |
| `~/.whodunit/data/journal.db` | One row per agent edit, scoped by repository |
| `~/.whodunit/baselines/<repo>.json` | Pre-adoption snapshots |

Everything is owner-only — `0700` directories, `0600` files — and repaired
on open if it was ever created more permissively. On Windows, where
`os.Chmod` cannot express that, the files carry an access control list
naming only the owner.

Nothing is ever written into your repositories, committed or pushed.

```sh
dun journal show    # exactly what has been recorded, in full
dun journal purge   # delete it, for this repository only
```

## What `dun sync` sends

`dun sync` is the one command that transmits anything. It is never run for
you and does nothing until you configure a target.

Six tables, listed in full so there is no need to take this on trust:

| Table | Contents |
|---|---|
| `whodunit_repos` | Repository id, **contributor email**, spec version |
| `whodunit_commits` | SHA, timestamp, status, method, agent, purpose, ratio, line and file counts |
| `whodunit_events` | Per edit: timestamp, agent, session, tool, **file path**, lines, hunk hash, outcome, model, branch, MCP server, human-edited |
| `whodunit_sessions` | Per session: message and tool counts, tokens, timing, effort, permission mode |
| `whodunit_event_lines` | Line hashes and first-seen timestamps |
| `whodunit_baselines` | Pre-adoption commit counts, median diff size, revert rate, cadence |

The repository id is its **root commit SHA** — stable across clones and
machines, and identifying the repository without revealing its name or
remote. A filesystem path would break when the repository moved; a remote
URL would record the org and repository name, which this tool never stores.

Not sent: prompt text, message content, file contents, and the lines
themselves.

```sh
dun sync --dry-run   # print the exact payload before any target is contacted
```
