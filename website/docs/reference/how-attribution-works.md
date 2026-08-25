---
id: how-attribution-works
title: How attribution works
sidebar_label: How attribution works
---

# How attribution works

## The sequence

1. **The agent works.** Claude Code, Codex and `agy` each write a session
   transcript for their own purposes — session resume, history. whodunit
   only reads them; it does not ask the agent for anything and does not
   change how the agent runs.

2. **`dun ingest` reads them into a local journal.** One row per edit:
   timestamp, agent, session, tool, file path, lines added and removed, a
   hash per produced line, and the outcome. This happens automatically at
   commit time, and continuously if you run `dun daemon run`.

3. **You commit.** The `prepare-commit-msg` hook looks up which staged
   files were touched by a recent session, compares the staged lines
   against the hashes the agent produced, and writes the trailer.

4. **`commit-msg` validates** what was written, so a malformed trailer
   fails at commit rather than at analysis time.

## How the confidence level is decided

```
staged files match a recent session?
├── no  → undetermined
└── yes → observed
         └── staged lines match lines the agent produced?
             └── yes → intersected
```

`observed` means the agent edited these files recently. `intersected` means
the exact text it produced is what got staged — the text was not rewritten
in between.

`declared` and `inferred` sit below `observed` on the ladder and are
reserved for agents whose stores cannot support file-level or line-level
evidence.

That tree decides *which* state a commit gets. What each state entitles you
to claim — and the four distinct reasons a commit can end up with no state
at all — is
[What the numbers mean](what-the-numbers-mean#six-states-and-only-one-of-them-is-a-problem).

## Why hashes rather than commit SHAs

Attribution is matched by **content hash, never by commit SHA**.

A commit does not exist when the observation is recorded, and once it does
it may be amended, rebased, squashed or cherry-picked. Hashing what
changed, rather than where it landed, keeps attribution correct across all
four.

The hashes are one-way. They confirm that a line the agent produced is the
line that shipped; they cannot reconstruct the line.

## The lookback window

A journal entry counts toward a commit for **30 days**.

Seven days was the original value and lost anything that sat over a holiday
or a long-running branch — which is exactly the work most likely to be
agent-heavy. The wider window costs about 26ms per commit, roughly 2% of
the hook's budget, because the query is indexed.

Line-hash retention is derived from the same constant rather than
configured separately. Pruning hashes the hook would still have matched
would silently turn an `intersected` commit into an `observed` one, so
`retention_days` cannot be set below the lookback.

## What happens when it fails

**Hooks never fail a commit.** A hook that blocks work because it could not
attribute it would be uninstalled within a day, so every failure path exits
zero and stamps `undetermined`.

Which is why `undetermined` alone cannot tell you whether anything went
wrong: a genuine failure and a commit nobody used an agent on both land
there. `dun status` separates them under **why undetermined**.

That silence is deliberate but not total — the errors are recorded:

```sh
dun log
```

shows what the hooks did and every error they swallowed.

## Verifying it end to end

```sh
dun verify
```

Checks the install, the hooks, the journal, the agents it can find, and the
sync target if one is configured — and names what to fix rather than
reporting a bare pass or fail.
