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
AI-Attribution: v=2; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

Once that trailer is on your commits you can answer, from evidence rather
than estimate: how much of this codebase an agent actually wrote, which
agent and which model, what it cost in tokens, and whether that work came
back as rework. Every answer is attached to a specific commit, checkable by
anyone with the repository, and impossible to add after the fact without
rewriting history.

`method=intersected` in that trailer is the strongest thing whodunit can
say, and it is the one word worth understanding before anything else:

![Two overlapping circles. The left holds the lines the agent produced, the right holds the lines in the commit, and the lit overlap between them holds the lines that are in both - which is what `intersected` means.](/img/architecture/the-intersection.png)

Every substantive line an agent writes is hashed as it writes it — blank
lines and lone braces are skipped, because a `}` proves nothing about who
wrote it. At commit time the staged diff is hashed the same way, and the
two sets are intersected. If the overlap is non-empty, the agent's own
text is demonstrably in the commit — not guessed from coding style, not
assumed from a licence someone holds.

## What you get

**Adoption you can defend.** Seven Grafana dashboards read the trailers:
adoption over time by agent and by model, cost per delivered line, cycle
time, DORA metrics beside the AI figures rather than instead of them. Every
rate is shown with the denominator it was computed over.

**Numbers that survive being questioned.** Each figure states the evidence
behind it. `intersected` means the agent's own lines are in the diff;
`unassisted` means the hooks were watching and a human wrote it. Where the
tool cannot know, it says which kind of not-knowing — the commit predated
instrumentation, or an agent was working elsewhere, or attribution itself
failed. Six states, so a fault and a finding never look alike.

**Cost in the unit you actually pay.** Tokens per delivered line, not a
dollar figure derived from a rate you may not be on. Multiply by your own
contract when you need currency.

**Nothing leaves your machine unless you send it.** Collection is local and
makes no network calls. The journal records which agent touched which file
— no prompt text, no keystrokes, no file contents, and no schema field that
could hold them. `dun sync` publishes to your own database when you
configure it. See [Privacy](reference/privacy).

**A change-size difference, not a productivity percentage.** whodunit
reports what assisted and unassisted work look like side by side, with the
cohorts visible, and stops short of a single "AI made us X% faster" number
— because assisted work is self-selected, and that percentage would be
measuring which tasks people chose to hand over. The full method is on
[How this measures AI's effect](measuring-ai-impact); the shorter list of
things people misread is [What the numbers mean](reference/what-the-numbers-mean).

## How it works, in three parts

![How attribution is established: agent transcripts are read by an adapter into a local journal and a set of line hashes; at commit time those hashes are intersected with the staged diff to decide one of five methods, or one of four reasons there is no method; the result is stamped as a git trailer and optionally synced to DevLake and Grafana.](/img/architecture/attribution-flow.png)

The middle of that diagram is the whole product. Everything else moves data
around; the intersection is where a claim gets made or withheld.

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
