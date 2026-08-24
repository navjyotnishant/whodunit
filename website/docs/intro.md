---
id: intro
title: What whodunit is
sidebar_label: What whodunit is
sidebar_position: 1
slug: /
---

# What whodunit is

whodunit records **which AI agent touched which code**, as a plain git
trailer on every commit:

```
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

It exists because the interesting claims about AI-assisted development —
how much of the codebase it wrote, what it cost, whether the work came back
as rework — are made without evidence. A trailer is evidence: attached to a
specific commit, checkable by anyone with the repository, and impossible to
add retroactively without rewriting history.

## What it is not

**It is not a productivity measurement.** This is the most important
sentence on the site, so it is near the top rather than in a footnote.

whodunit can tell you that 143 commits carry agent attribution and that
they consumed 7.5 billion tokens. It cannot tell you those commits were
faster, because assisted and unassisted work are self-selected: people
reach for an agent on some kinds of task and not others. A throughput
difference between the two groups may be measuring which work was chosen,
not what the agent did.

The tool reports cost and cycle time per delivered unit of work, with the
denominators visible, and refuses to collapse that into a percentage.

How that is computed — the formula, what each term resolves to, and what it
deliberately will not claim — is on
[How this measures AI's effect](measuring-ai-impact). The shorter list of
things people misread is [What the numbers mean](reference/what-the-numbers-mean).

**It is not surveillance.** It answers "which agent touched this code", not
"who wrote this line". No prompt text, no keystrokes, no file contents are
recorded — the journal schema has no field that could hold them. Collection
is entirely local and makes no network calls; data leaves your machine only
if you configure `dun sync` and run it. See [Privacy](reference/privacy).

## How it works, in three parts

**The trailer** is a specification, not an implementation. It is plain git,
readable by anything that reads a commit message, and it outlives any one
tool that writes it.

**The collector** (`dun`) fills the trailer in automatically. It reads the
session transcripts your agent already writes for its own purposes, matches
them against what you staged, and stamps the commit at commit time. Three
agents are supported today, and they do not report the same things — see
[Agent capabilities](reference/agent-capabilities) before you draw
conclusions from an empty panel.

**The dashboards** are optional. `dun sync` publishes into an Apache DevLake
database, and seven Grafana dashboards read it. Everything before this step
works with no network and no server.

## Start here

- [Install](getting-started/install) — Homebrew, Scoop, or a plain archive
- [Your first commit](getting-started/first-commit) — instrument a repository and watch a trailer appear
- [Reading the trailer](getting-started/the-trailer) — what each key means and how much to trust it

---

Built by **Navjyot's Lab**. Apache-2.0, and the source is on
[GitHub](https://github.com/navjyotnishant/whodunit).
