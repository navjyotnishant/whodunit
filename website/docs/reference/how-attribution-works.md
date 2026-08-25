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
├── no  → was the agent producing lines at all?
│         ├── no  → unassisted    (a human wrote this)
│         └── yes → unmatched     (the agent was working elsewhere)
└── yes → observed
         └── staged lines match lines the agent produced?
             └── yes → intersected
```

`observed` means the agent edited these files recently. `intersected` means
the exact text it produced is what got staged — the text was not rewritten
in between.

The left branch is the part worth reading twice. Both outcomes produce no
attribution, and they are not the same thing: `unassisted` is a finding —
the hooks were watching, the journal was readable, and there was no agent
— while `unmatched` means an agent was busy somewhere the staged files
are not. A script that generates a file leaves no tool call naming it, so
its commits land in `unmatched`, and claiming those lines for the agent
that ran the script would be a lie about who wrote them.

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
zero and stamps a status saying what happened.

`degraded` is the one that means something broke — an unreadable journal,
a failed ingest, config that would not load. It is deliberately distinct
from `unmatched` and `unassisted`, which are answers rather than faults.

Failures are also appended to a **replay log** that nothing rotates away,
so a determination that failed can be retried once the cause is fixed:

```sh
dun replay           # what failed, and why
dun replay --apply   # retry each one against the journal as it stands now
```

A retry re-runs the same determination against a journal that may have
learned more since — a transcript ingested after the commit was made
carries hashes that were not available at the time. The commit keeps its
original trailer either way: git history is not rewritten, and the replay
log records the outcome by appending rather than by editing what it
already said.

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
