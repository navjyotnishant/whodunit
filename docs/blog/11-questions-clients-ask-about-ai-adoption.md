---
title: "The AI adoption dashboard that refuses to lie"
published: false
description: "Clients keep asking for an AI productivity percentage. Here is the tool I built to answer the questions evidence supports, and to refuse the one it cannot."
tags: ai, metrics, opensource, devops
---

A client buys seats for an AI coding tool. Six months later somebody in finance asks what it bought. That question lands on me.

What usually gets shown at that meeting is a survey that measures how people feel about a tool they were told to adopt, or a vendor dashboard that counts seats. Neither is evidence about the codebase, and nobody in the room believes either.

So I started writing down the questions clients actually ask, verbatim, as they ask them. Over a couple of years the same eleven came back.

1. What has the AI adoption been for my team — is it consistent across everyone, or can we identify inconsistencies?
2. Show me the productivity improvement after AI adoption.
3. Is everyone using AI efficiently, or just as a chatting tool? Do they know compaction? Do they know when to choose which model? Do they know the importance of skill files, agents, sub-agents? Who is not using the AI tools at all?
4. How are the AI tools being used — to supplement code, or to write the whole thing?
5. What has been the acceptance rate of code written by AI?
6. Is there a view showing where AI is being used — design, bug fixes, features, tests?
7. What kind of autonomy are users granting to AI while coding?
8. Is there a way to see code churn over time for code written by AI?
9. Which AI model is being used the most, if the org runs more than one?
10. Was there any improvement in PR cycle times?
11. How long did adoption take for my team — and does it differ by model?

I built [whodunit](https://github.com/navjyotnishant/whodunit) to answer these from evidence rather than assumption. Most of them it answers. Some it answers partially, and I'll say which part. And one — number 2, the productivity percentage — it deliberately refuses to answer the way clients want it answered.

**That refusal is the spine of this post.** A tool that gives you a productivity number from this data is lying to you, and I'd rather explain why than ship the lie.

## The source of truth is commits, not sentiment

Surveys tell you how people feel. Seat dashboards tell you who has access. Neither proves that AI-generated work reached the codebase, survived human editing, or changed delivery.

Every commit can carry a plain git trailer:

```text
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

Readable by anything that reads a commit message. It travels with the repository, and it cannot be quietly added later without rewriting history.

The important field is `method`. At the strongest level, `intersected`, there is evidence that text the agent produced is the text that ended up staged. At weaker levels it says so. When evidence is missing it records `undetermined` rather than pretending that "no evidence" means "no AI."

**That distinction, between absence and zero, is the whole philosophy of the project.**

## What you see first

![Whodunit — Executive Summary](https://navjyotnishant.github.io/whodunit/img/dashboards/executive-summary.png)

This dashboard exists for the person who will not open the other six: assisted commit count, attribution coverage, acceptance rate, active sessions, and the trend of each, on one screen. In practice that's the person asking the question in the first place.

It does not try to win the argument by making the number bigger. It tries to make the denominator visible.

Coverage and adoption are deliberately separate, and they get confused constantly:

- **Coverage** is the share of commits carrying a valid trailer of any kind, including `undetermined`.
- **Adoption** is the share carrying `status=assisted`.

A repository at 100% coverage may have had no agent involvement whatsoever. Coverage says the instrumentation is working; adoption says the agents are being used.

## 1. Is it being adopted, and evenly? (Q1, Q11)

![Whodunit — Adoption](https://navjyotnishant.github.io/whodunit/img/dashboards/adoption.png)

This is the denominator dashboard — where you check whether a figure elsewhere rests on three sessions or three hundred.

The panels that matter are **By contributor** and **Committed work by contributor**. Identity comes from the git committer email already in every commit, so this makes existing information easier to query rather than observing anything new. The second panel deliberately shows coverage alongside, because a penetration figure without coverage tells you how much of *what we could see* was assisted, not how much of the work was.

That answers the practical management question: is adoption evenly distributed, or are two people carrying the whole number? You can see the spread, and you can see who has no assisted commits at all.

For adoption over time there's a per-agent curve — it lives on the AI Impact on Delivery dashboard rather than this one. An agent gets credit for a commit when it recorded activity in that repository within 24 hours before the commit. That's a heuristic and it's labelled as one: a commit worked on by two agents appears in both series, so the lines don't sum to the overall rate.

**The limit.** Q11 asks how long adoption took *by model*. What you get is a per-agent curve, not a per-model one. And there's a harder problem underneath: the curve starts when the hooks were installed, not when people started using AI. Everything before instrumentation is dark. That's why `dun baseline capture` exists — the real before-and-after window closes as soon as hooks start stamping commits.

## 2. Are they using it well, or just chatting? (Q3, Q9)

![Whodunit — Cost & Efficiency](https://navjyotnishant.github.io/whodunit/img/dashboards/cost-efficiency.png)

This is the question I get asked most aggressively and the one I have to be most careful about, because it's really five questions wearing a trenchcoat.

**Let me be blunt about what this does not measure.** `dun` does not know whether a developer understands context compaction. It does not know whether they picked the right model for a task. It has no visibility into whether they've written skill files, or configured sub-agents, or thought about their workflow at all. There is no panel for "does this person know what they're doing", and I'm not going to build one from proxies and call it competence.

**What's left is the honest half: the "just chatting" part is answered by proxy, and the proxy is a decent one.**

**Session shape** carries most of the weight. The funnel's Stage 2 buckets sessions by what actually happened in them, from conversation-only up to agentic work using MCP or many tools. That's depth rather than count, deliberately, because ten sessions that produced nothing and one that rewrote a subsystem are not the same adoption.

![Whodunit — AI Productivity Funnel](https://navjyotnishant.github.io/whodunit/img/dashboards/productivity-funnel.png)

**Compaction** gets you partway. Measured on my machine, 92% of turns ran above 150k context while a small fraction of sessions ever compacted. That's suggestive. It is not "they don't know about compaction" — a short session has nothing to compact, which is exactly why the panel is deliberately not colour-coded.

**Which models, and what their tokens are made of**, answers Q9 directly and well. The *Token mix by model* panel stacks each model's tokens as a percentage rather than absolute counts, because cache reads were 88–99% of every model's total on my own data — an absolute stack would draw one long bar with three invisible slivers.

So the "who isn't using it at all" part is answered cleanly. The "do they know compaction / model choice / skill files" part is **not answered**, and if a vendor tells you their dashboard answers it, ask them which field in which transcript they're reading.

## 3. Supplement, or writing the whole thing? (Q4)

The trailer's `ratio` field exists for this. A commit with `ratio=0.62` means roughly 62% of the change is attributable to the agent under the recorded method.

The ratio is only as strong as the `method` beside it:

- `intersected` — agent-written lines survived into the staged diff.
- `observed` — the agent touched the file, but the text changed afterwards.
- `inferred` and `declared` are weaker.
- `undetermined` — the evidence isn't there.

That distinguishes "AI helped with a few lines" from "AI produced most of this change" without pretending every assisted commit is the same. It keeps the argument anchored in code that landed, not prompts that sounded productive.

## 4. Where is it being used? (Q6)

The *AI-assisted work by purpose* panel — on the executive summary above — derives purpose from Conventional Commit prefixes and path heuristics. And here's the caveat printed on the panel itself:

> Purpose is a label, not an observation.

`feature` is reachable only through a literal `feat:` prefix, so a repository not using Conventional Commits will show almost none — which says nothing about the work itself. If your team doesn't use Conventional Commits, this panel is close to useless, and I'd rather tell you that up front than have you quote it in a board deck.

This is one of the places where I'd rather have a less impressive dashboard and a more honest caption.

## 5. What autonomy are people granting? (Q7)

This turned out to be the most interesting one to build.

Different agents use different permission vocabularies. Codex reports modes like `never` and `on-request`; Claude Code reports `acceptEdits`, `auto` and `default`. whodunit keeps each agent's own vocabulary rather than flattening them into one shared enum — asserting that `never` means the same thing as `default` would be a claim I can't support.

The useful metric is tool calls per session, because sessions and total calls run in opposite directions. Measured here, 64 sessions on Codex's `never` mode produced 506 calls, while 2 Claude Code sessions on `auto` produced 6,766. Charting both put a 13x range beside a 32x one and flattened whichever shared the axis. Dividing gives the thing both were circling: **8 calls per session on `never`, 3,383 on `auto`.**

**The caveat matters: tool calls are not shipped work.** A mode that permits more actions produces more actions by construction. Sample sizes here are thin and the panels say so — `default` is 3 sessions and `auto` is 1 against 64 for `never`, and a mode with one session tells you about that session.

## 6. What did humans actually keep? (Q5)

Acceptance rate is one of the cleanest things the tool reports — shown per tool, so a low overall rate can be traced to which tool is being turned down, and always with its denominator on the same row. A 90% rate over 10 calls and a 60% rate over 10,000 calls are not the same finding.

Agents expose this differently, and one of them only started reporting it once I found a bug in my own adapter: it was hardcoding every outcome to "unknown" while ignoring a `steps.status` column that was populated on every row all along. The acceptance stage of that agent's funnel was structurally blank, and the docstring said the data didn't exist. It did. I hadn't read it.

There's a stronger signal where available: *Human edited the agent's output* is the difference between "the agent wrote this" and "the agent wrote this and it was kept". Only Claude Code reports it, out of the three agents supported. The denominator counts only calls that carried the signal, so agents without it don't dilute the rate.

## 7. Did delivery actually change? (Q2, Q8, Q10)

![Whodunit — AI Impact on Delivery](https://navjyotnishant.github.io/whodunit/img/dashboards/ai-impact-on-delivery.png)

This is where the conversation turns from interesting to dangerous.

**Code churn (Q8)** is a panel: lines written and removed by agents per day, from observed edits rather than commits.

**PR cycle time (Q10)** is there — median cycle time, open to merge, with the PR count beside it and a note to check that count before reading much into it. The executive summary carries the comparison directly: median cycle for AI-assisted work against the rest, with both sample sizes shown.

And then **Q2. Show me the productivity improvement.**

There is a panel on this dashboard whose entire content explains why that number is not there. It is titled *"Why this is not a productivity percentage"*.

The reason is selection bias. Assisted and unassisted commits in the same period are not two randomised groups doing the same work. Developers choose when to reach for an agent — on larger changes, repetitive migrations, unfamiliar code, test generation. Measured on this data, 48% of assisted commits are features against 38% of unassisted ones, so an unmatched comparison partly measures which work got assigned to the agent.

What you get instead is *Change size, assisted vs not*: mean lines changed per assisted commit against unassisted, within purposes where both sides have at least 20 commits — so `feature` isn't compared against `docs`.

So if assisted commits are larger, what did we learn? Maybe the agent takes on bigger work. Maybe it writes more code for the same outcome. Maybe it was used on work that was already different. **All three are consistent with the same chart.** That panel is deliberately not colour-coded, because green for positive would assert that larger commits are better — the exact claim the panel beside it exists to refuse.

I know this is not what the client wanted. I've had the conversation where I explain that the number they asked for cannot be honestly produced from this data, and it is not a fun conversation. It's still the right one.

## The metric traps that make AI look better than it is

The dangerous errors are the flattering ones, because nobody questions a number that looks good.

### Empty is not zero

A missing measurement is recorded as absent, never as zero. A `NULL` token count means the agent doesn't report tokens; a blank panel means nothing measured it.

Zero is a *value*. Averaged, charted or summed, it claims that an agent cost nothing or that no AI was involved. Absence makes no claim at all. This is why `status=undetermined` exists instead of a "no AI" value, and why Antigravity is excluded from cost panels rather than shown at 0 — it records no tokens, so a 0 would make it look like the cheapest agent available.

Reporting absence as "no AI was used" is the cardinal error with this data.

### Tokens are not dollars

There is no dollar figure anywhere in the tool, and that's a decision rather than a gap.

Under a subscription the marginal cost of a token is zero — someone on a fixed monthly plan spends the same whether a session burns 10k tokens or 10M. Multiplying their tokens by an API rate would report money nobody spent. Nothing in any transcript says which billing model a user is on, so the tool would be guessing at the pricing model before it even reached the price.

If you need currency, multiply by your own contract. The tool should not guess.

### Cache writes belong in the denominator

```text
cache_read / (uncached_input + cache_write + cache_read)
```

A cache write arrives uncached and is billed *above* base rate, so leaving it out flatters the result badly.

I'll confess this one with my own data, because I made the mistake: the first version of my analysis reported a 99% hit rate and concluded caching needed no attention. Recomputed with writes included, the real figure was **47.6%**. A panel reporting 99% recommends nothing. The flattering error is also the one that makes the dashboard useless.

### Write payback breaks even at 1.25x, not 1.0x

A write costs about 1.25x base and a read about 0.1x, so a write needs roughly 1.25 reads to pay for itself. A series sitting at 1.10x lost money while looking fine against a 1.0 line.

The dashboards draw break-even at 1.25 and use a log axis, because the measured spread ran from 0.73x to 21x. Report it per model, never as one number: the aggregate here was a healthy 3.60x while one model sat at 0.73x — a loss, entirely invisible in the total.

### A fix rate is not a defect rate

The rework panel compares how often assisted and unassisted commits are fixes, but `purpose` comes from commit prefixes and path heuristics — so it measures what commits were *labelled*, not what actually broke.

Even that proxy flips when you band by change size. Unbanded it read 28.4% assisted against 33.3% unassisted, which looks favourable. Split by size, the middle band reversed it: **42.1% against 24.7% at 50–300 lines.** The aggregate was a size-mix artefact.

### A dashboard can fail silently

This one isn't about arithmetic. DevLake can be misconfigured — a wrong deployment pattern classifies nothing, and the panel renders empty rather than erroring. An empty panel looks exactly like "nothing happened."

Silent emptiness is another way metrics lie, which is why the setup docs name the failure modes instead of assuming a clean install.

## How it's built

Two independent components. **You can use the first without ever touching the second.**

### `dun`, the local CLI

```bash
cd your-repo
dun init          # installs prepare-commit-msg, commit-msg, pre-push hooks
git commit ...    # trailer gets stamped automatically
dun status        # coverage + method mix for recent commits
dun report        # self-contained HTML report, opens in any browser
```

`dun init` is once per repository, and it chains to any hook you already have rather than clobbering it. There is no flag to enrol every repository on the machine, because that set includes client work, throwaway experiments, and clones of other people's projects. Stamping attribution trailers into those is a disclosure decision that belongs to you, one repo at a time.

Attribution comes from session transcripts the agents already write for their own purposes — Claude Code's JSONL projects, Codex's JSONL rollouts, Antigravity's local SQLite store. Matching is by content hash, never by commit SHA: a commit doesn't exist yet when the observation is recorded, and may later be amended, rebased or squashed. Hashing what changed rather than where it landed keeps attribution correct across all three.

**`dun report` renders a self-contained HTML file with no server and no network.** `dun sync` is the only command that transmits anything, and `dun sync --dry-run` prints the exact payload before any target is contacted.

### The optional DevLake layer

For teams that want a shared view across people and repositories. Docker compose: one script builds the stack, a second imports the dashboards. They're separate because step 2 is re-run on every whodunit release and step 1 isn't.

Seven Grafana dashboards, 140 panels: adoption, cost and efficiency, delivery impact, executive summary, productivity funnel, activity hours, and attribution detail.

**Why DevLake, when nothing here uses DevLake?** Its own collectors are unused — whodunit's data comes from git and the local journal, not from GitHub or Jira, and a bare Postgres and Grafana would serve the same panels. DevLake is the target because a team already running it should be able to add whodunit without standing up a second stack. That only works if the tables coexist with theirs, which is why every table is prefixed `whodunit_` and nothing writes to DevLake's domain tables — their schema shifts between minor versions, and writing into it would make every upgrade a potential data-loss event.

## Privacy, without handwaving

The collection path makes no network calls. Hooks, the daemon and `dun ingest` read git and local transcripts and write a local journal under `~/.whodunit`.

**The journal schema has no field that could hold prompt text, message content, file contents, hostnames or remote URLs.** That's a structural guarantee rather than a filtering step — there is nowhere to put them. Things considered and rejected by name: Claude Code's `toolUseResult` as a whole (it carries stdout, stderr, and the before/after text of every edit), Codex's `agent_message.message` and `unified_diff`, Antigravity's `CodeContent` and `ReplacementContent`. MCP tool-call arguments and web-search queries are countable but never stored, because they carry user intent verbatim.

**Now the awkward part, stated plainly**, because a privacy page that omits it is worse than none:

- **File paths.** The journal stores which files an agent edited. That reveals what someone worked on, not merely how much.
- **A contributor email.** `whodunit_repos` stores the git committer email — the same address already in every commit you author. A shared database needs it to attribute anything to anyone.

Both stay local until you run `dun sync`. Everything under `~/.whodunit` is owner-only — `0700` directories, `0600` files, repaired on open if they were ever created more permissively. Nothing is ever written into your repositories.

## The answer I'm comfortable giving

If a client asks whether people are adopting AI tools, I can answer.

If they ask which agents and models are being used, I can answer where the tools report it.

If they ask whether AI is reaching committed code, I can answer with evidence strength.

If they ask where it's being used, I can answer within the limits of commit labels and paths.

If they ask whether PR cycle time moved, I can answer when delivery data is connected and the sample is large enough.

If they ask me to show them the productivity improvement, I will not give them a fake percentage.

The best version of this project is not the one that makes AI look good. It's the one that makes the evidence hard to misuse — it prints the denominator, it leaves missing values empty, and it refuses to convert correlation into causation because the slide would look better.

**And if any of the metric logic is wrong, I want the argument.** The 1.25x break-even, putting cache writes in the denominator, banding the fix rate by change size, the productivity refusal itself. Those are the parts I'm least confident in and most interested in criticism of.

The goal is not to win the AI adoption narrative. It's to stop lying to ourselves with prettier charts.

Code: [github.com/navjyotnishant/whodunit](https://github.com/navjyotnishant/whodunit)
Docs: [navjyotnishant.github.io/whodunit](https://navjyotnishant.github.io/whodunit/)

Issues and arguments both welcome.
