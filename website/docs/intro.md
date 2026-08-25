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

`method=intersected` in that trailer is the strongest thing whodunit can
say, and it is the one word worth understanding before anything else:

![Two overlapping circles. The left holds the lines the agent produced, the right holds the lines in the commit, and the lit overlap between them holds the lines that are in both - which is what `intersected` means.](/img/architecture/the-intersection.png)

Every substantive line an agent writes is hashed as it writes it — blank
lines and lone braces are skipped, because a `}` proves nothing about who
wrote it. At commit time the staged diff is hashed the same way, and the
two sets are intersected. If the overlap is non-empty, the agent's own
text is demonstrably in the commit — not guessed from coding style, not
assumed from a licence someone holds.

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

![How attribution is established: agent transcripts are read by an adapter into a local journal and a set of line hashes; at commit time those hashes are intersected with the staged diff to decide one of five methods, or one of four reasons there is no method; the result is stamped as a git trailer and optionally synced to DevLake and Grafana.](/img/architecture/attribution-flow.png)

The middle of that diagram is the whole product. Everything else moves data
around; the intersection is where a claim gets made or withheld. Two things
it shows are designed rather than built — the replay log, and the four
reasons as first-class states rather than reconstructed after the fact.

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
