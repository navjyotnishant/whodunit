---
title: "11 questions clients ask about AI adoption, and what I can actually answer"
published: false
description: "One question clients ask about AI coding tools can't be honestly answered from this data. The open-source tool I built, and why it refuses."
tags: ai, metrics, opensource, devops
---

> **TL;DR.** Clients keep asking what their AI tooling bought them. I built [whodunit](https://github.com/navjyotnishant/whodunit), an open-source tool that stamps a git trailer on every commit recording which agent touched it and how strong the evidence is, then reads it back into seven Grafana dashboards.
>
> Of the eleven questions I get asked, most are answerable from commits and local agent transcripts: adoption per person, acceptance rate, model mix, autonomy, churn, where AI is used. Cycle time is answerable if your delivery data is wired up.
>
> **Two are not.** Whether your developers understand compaction is not measurable from a transcript. And the productivity percentage everyone wants cannot be derived honestly, because assisted and unassisted work are self-selected. The tool reports a difference and refuses to call it a gain.
>
> The metric definitions are the part I most want argued with. Skip to [What these numbers don't claim](#what-these-numbers-dont-claim) if that is what you came for.

During my conversations with clients I have observed a generic pattern: most of the conversations are around AI productivity, AI investment and ROI, AI versus human throughput and accuracy, and most importantly, how do we measure it. Everyone has their own way of measuring it, and none of those feels solid. 

This is my attempt at an answer: a lightweight tool I wrote over a weekend, called whodunit.

First of all, let me summarise some of the common questions I have heard over time.

## Questions

1. What has the AI adoption been for my team: is it consistent across everyone, or can we identify inconsistencies so necessary training can be planned?
2. Show me the productivity improvement after AI adoption: how many tickets did users close before and after, how many commits before and after?
3. Is everyone in the team (or is every team) using AI efficiently, or just as a chatting tool? Do they know how to use it efficiently? When to choose which model? The importance of skills? Are they leveraging agents? Sub-agents? Do they understand the importance of regular compaction? Do they know what cache and memory are used for, and how those can help optimise cost? Can they translate day-to-day work into an efficient workflow? Who is not using the AI tools at all?
4. How are the AI tools being used: to supplement code, or to write the whole thing?
5. What has been the acceptance rate of code written by AI?
6. Is there a view showing where AI is being used across design, bug fixes, features and tests?
7. What kind of autonomy are users granting to AI while coding?
8. Is there a way to see code churn over time for code written by AI?
9. Which AI model is being used the most (if the org runs more than one)?
10. Was there any improvement in PR cycle times?
11. How long did adoption take for my team, and does that differ by model?


I built [whodunit](https://github.com/navjyotnishant/whodunit) to answer these from evidence. This is an effort to answer most of them with the data already present in PM tools (Jira, Linear and the like) and the AI agent logs already sitting on everyone's machine.

The mechanism is a git trailer on every commit:

```plaintext
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

Plain git. Readable by anything that reads a commit message, and impossible to add retroactively without rewriting history. `method` records how strong the evidence is, from `undetermined` up to `intersected`, which means the exact text the agent produced is what ended up staged.

## The one-screen version

![Whodunit - Executive Summary](https://navjyotnishant.github.io/whodunit/img/dashboards/executive-summary.png)

The executive dashboard gives you a quick eagle-eye view: how much AI is being adopted, how fast that adoption moved, where it is landing, and whether delivery looks any different alongside it. One screen, before anyone opens the other six.

Here is what each number is counting, and what you can honestly say from it:

- **Adoption.** Assisted commits divided by all commits in the window. *Says: how much of what shipped had an agent involved.* It does not say how much of the work AI did; a commit is assisted if any of it was.
- **Session depth.** Sessions bucketed by what happened in them: `agentic` if the session used MCP or five-plus distinct tools or fifty-plus calls, `edits` if it called any tool at all, `chat only` otherwise. *Says: whether people are running workflows or just talking.* This is the closest thing here to answering "are they using it well", and it is a proxy.
- **Acceptance.** Accepted edits over accepted plus rejected plus failed. *Says: how much of what the agent proposed a human kept.* Agents that report no accept/reject signal are excluded from the denominator rather than counted as accepted.
- **Median cycle, assisted vs other.** Median lead time for issues whose commits carry an assisted trailer, against those that don't, on the same board. *Says: how long each cohort took.* Median rather than mean, because one two-week ticket drags an average and does not move a median.
- **Assisted advantage.** The difference between those two medians. *Says: assisted issues closed faster in this window.* 
- **Commits by month.** Assisted and unassisted stacked to the month's total, with assisted share on the right axis. *Says: when adoption actually happened.* Months before instrumentation are full unassisted bars, which is a measurement; a month with no commits is absent entirely, which is not the same thing.

Every rate on this screen refuses to render below a minimum cohort. The cycle-time cards need at least ten issues on each side of the comparison; below that they show `n/a` and say why, instead of dividing one ticket by four and printing it to one decimal place. An empty card is a worse slide and a better number.

---

## Theme 1: Is it being adopted, and evenly? (Q1, Q11)

![Whodunit - Adoption](https://navjyotnishant.github.io/whodunit/img/dashboards/adoption.png?v=2)

Start here, always. This is the denominator dashboard, and it answers the question you should ask before believing any figure on any other screen: is this resting on three sessions, or three hundred?

The top row is the denominators themselves: coverage, how many contributors and repositories, how many sessions, how many days had any activity at all. Read those before anything else on any screen.

For Q1, drop to the **Who and what** row at the bottom. **By contributor** shows per-person activity, and before you worry about the surveillance implications, note where the identity comes from: the git committer email that is already in every commit you have ever authored. Nothing new is being observed here. It is the same information, made easier to query.

**Committed work by contributor** sits beside it, and it is the one I would actually put on screen in a meeting, because it shows coverage right next to the penetration figure. That pairing matters more than it sounds. Without coverage, a penetration number tells you how much of *what we could see* was assisted, which is a different claim from how much of the work was.

**That distinction is the whole answer to "is it consistent across everyone".** You get the spread, and you get the names with no assisted commits at all.

Q11 asks how long adoption took, and that curve is not on this screen. **Adoption over time**, split by which agent was active, lives on the delivery dashboard further down this post. Worth knowing how it decides: an agent gets credit for a commit when it recorded activity in that repository in the 24 hours before it. That is a heuristic, and I would rather label it than dress it up, so: a commit two agents touched shows up in both series, and the lines are not meant to sum to the overall rate.

**Where it runs out.** Q11 really asks about models, and what you get is a curve per agent, not per model. There is a harder problem sitting underneath that one, though, and it is worth understanding before you install anything: the adoption curve begins when the hooks went in, not when your team started using AI. Everything before instrumentation is simply dark.

If you want the real curve, you have to capture a baseline *before* you start. `dun baseline capture` exists for exactly that reason, and the reason it is a separate command you run first is that this particular window closes permanently. Miss it and no amount of later cleverness gets it back.

---

## Theme 2: Are they using it well, or just chatting? (Q3, Q9)

![Whodunit - Cost & Efficiency](https://navjyotnishant.github.io/whodunit/img/dashboards/cost-efficiency.png)

This is the question I get asked most aggressively, and the one I am most careful answering, because it is really five questions wearing a trenchcoat. Read Q3 again and count them.

**So let me be blunt about what this cannot measure.** `dun` has no idea whether a developer understands context compaction. It cannot tell you whether they picked the right model. It has no visibility into skill files, sub-agents, or whether anyone has thought about their workflow at all. There is no panel called "does this person know what they're doing", and I am not going to assemble one out of proxies and let you present it as competence. If that is the number you need, this tool will disappoint you, and I would rather disappoint you here than in a performance review.

**What is left is the honest half, and it is more useful than it sounds.** The "just chatting" part *is* answerable by proxy, and the proxy holds up. Here is what it is made of.

**Session shape** carries most of the weight. The funnel's Stage 2 buckets sessions by what actually happened in them, from conversation-only up to agentic work using MCP or many tools. That's depth rather than count, deliberately, because ten sessions that produced nothing and one that rewrote a subsystem are not the same adoption.

**Compaction** gets you partway, and I want to show you how to read it carefully. There is a *Sessions that compacted* panel. On my own machine, 92% of turns ran above 150k context while only a small fraction of sessions ever compacted. Suggestive, isn't it? Resist the conclusion. A short session has nothing to compact, so a low number may mean people are working in tight bursts rather than that nobody knows the feature exists. That is precisely why the panel carries no red or green.

**Which models, and what their tokens are made of**, answers Q9 directly and well. The *Token mix by model* panel stacks each model's tokens as a percentage rather than absolute counts, because cache reads were 88–99% of every model's total on my own data. An absolute stack would draw one long bar with three invisible slivers.

**Who isn't using it at all** is answered too, via the contributor panels in Theme 1.

So the last part of Q3 comes out clean. The "do they know compaction, model choice, skill files" part does **not**, and here is the practical thing to take away: if a vendor tells you their dashboard answers it, ask them which field in which transcript they are reading. I have yet to hear an answer.

---

## Theme 3: Where and how is it being used? (Q4, Q6, Q7)

![Whodunit - AI Productivity Funnel](https://navjyotnishant.github.io/whodunit/img/dashboards/productivity-funnel.png)

Q4, supplement or write the whole thing, is what the trailer's `ratio` key is for: the fraction of the change attributable to the agent. Alongside it, `method` tells you how much to trust that number, and the *Evidence strength* panel on the delivery dashboard breaks it down: `intersected` means agent-written lines survived into the commit, `observed` means the agent touched the file but the text changed afterwards.

Q6, where it is being used, maps to the *AI-assisted work by purpose* panel, built from Conventional Commit prefixes and path heuristics. The caveat is printed on the panel itself, and it is worth reading twice: **purpose is a label, not an observation.** `feature` is only reachable through a literal `feat:` prefix. So a repository that doesn't use Conventional Commits will show almost no feature work, which tells you nothing whatsoever about the work.

If your team doesn't use Conventional Commits, this panel is close to useless to you. I would much rather say that here than have you quote it in a board deck and find out later.

Q7, autonomy, turned out to be the most interesting one to build, and the story of why is worth a paragraph. The *Autonomy and what it produced* panel, over on the delivery dashboard, reports sessions and tool calls at each permission level. One metric, not two bars, and the reason is that sessions and total tool calls run in opposite directions. Look at the numbers: 64 sessions on Codex's `never` mode produced 506 calls, while 2 Claude Code sessions on `auto` produced 6,766. Chart both and you put a 13x range beside a 32x one, flattening whichever loses the axis. Divide them and you get the thing they were both circling: 8 calls per session on `never`, 3,383 on `auto`.

**The finding is that the ladder is monotonic across every mode measured, autonomy isn't how often it's granted, it's how much happens once it is.**

One deliberate non-decision, because it would have made a tidier dashboard: each agent keeps its own vocabulary rather than being mapped onto a shared enum. Codex says `never` and `on-request`. Claude Code says `acceptEdits`, `auto` and `default`. Flattening those into one scale would assert that `never` means the same thing as `default`, and I cannot support that claim, so the dashboard doesn't make it.

**The limit.** Sample sizes here are thin and the panels say so; a companion panel notes that `default` is 3 sessions and `auto` is 1 against 64 for `never`, and states plainly that a mode with one session tells you about that session. Also: tool calls are not shipped work. A mode that permits more actions produces more actions by construction.

---

## Theme 4: Did delivery actually change? (Q2, Q5, Q8, Q10)

![Whodunit - AI Impact on Delivery](https://navjyotnishant.github.io/whodunit/img/dashboards/ai-impact-on-delivery.png)

Q5 and Q8 come out of the data most directly.

**Acceptance rate (Q5)** comes from accept/reject outcomes the agents record about themselves, broken out per tool so that a poor overall rate can be traced to whichever tool is actually being turned down. The denominator always sits on the same row, because a rate without one is a rumour.

There is a better signal hiding in here for one agent. *Human edited the agent's output* is the difference between "the agent wrote this" and "the agent wrote this and it survived", which is much closer to what you actually care about. Only Claude Code reports it, of the three agents supported, and the denominator counts only calls that carried the signal, so the agents that can't report it don't quietly dilute the rate.

**Code churn (Q8)** is a panel: lines written and removed by agents per day, from observed edits rather than commits. There's also churn by hour on the activity dashboard.

**PR cycle time (Q10)** is there, median PR cycle time, open to merge, with the PR count sitting next to it and a note to check that count before reading much into it. The executive summary carries the comparison directly: median cycle for AI-assisted work against median cycle for the rest, with both sample sizes shown.

And then there is **Q2. Show me the productivity improvement.**

This is the one. If you have read this far, you already know it is coming, and there is a panel on this dashboard whose entire content is an explanation of why that number is not there. It is titled *"Why this is not a productivity percentage"*. Its text says a productivity gain needs a before and an after of the same work, and what exists here is assisted against unassisted commits in the same period, two populations that are not the same. Measured on this data, 48% of assisted commits are features against 38% of unassisted ones, so an unmatched comparison partly measures which work got assigned to the agent.

What you get instead is *Change size, assisted vs not*: mean lines changed per assisted commit against unassisted, within purposes where both sides have at least 20 commits, so `feature` isn't being compared against `docs`. A positive number means assisted commits are larger. **Larger is not faster and not better:** it may mean the agent takes on bigger work, or that it writes more code for the same outcome. Both are consistent with the figure.

That panel is deliberately not colour-coded, because green for positive would assert that larger commits are better, which is the claim the panel beside it exists to refuse.

I know this is not what the client wanted. I have sat in that meeting and explained that the number they asked for cannot be honestly produced from this data, and I can tell you it is not a fun conversation to have. It is still the right one, and it is a better conversation than the one you have six months after someone acts on a made-up percentage.

---

## What these numbers don't claim

Every figure here has a definition that can be got wrong, and several of them are wrong in a *flattering* direction, which is the dangerous kind, because nobody questions a number that looks good.

**Empty is not zero.** This is the rule the rest of the tool is built on. A missing measurement is recorded as absent, never as zero. A `NULL` token count means the agent doesn't report tokens; a blank latency panel means nothing measured it. Zero is a *value*: averaged, charted or summed, it claims that an agent cost nothing or that no AI was involved. Absence makes no claim at all. This is why `status=undetermined` exists instead of a "no AI" value, and why Antigravity is excluded from cost panels rather than shown at 0. It records no tokens at all, so a 0 would make it look like the cheapest agent available. Reporting absence as "no AI was used" is the cardinal error with this data.

**Tokens, never currency.** There is no dollar figure anywhere in the tool, and that's a decision rather than a gap. Under a subscription the marginal cost of a token is zero, someone on a fixed monthly plan spends the same whether a session burns 10k tokens or 10M. Multiplying their token count by an API rate would report money nobody spent. Nothing in any transcript says which billing model a user is on, so the tool would be guessing at the pricing model before it even reached the price.

**Cache writes belong in the denominator.** The cache read ratio is `cache_read / (uncached_input + cache_write + cache_read)`. A write arrives uncached and is billed *above* base rate, so leaving it out of the denominator flatters the result badly.

I'll confess this one with my own data, because I made the mistake: the first version of my analysis reported a 99% hit rate and concluded caching needed no attention. Recomputed with writes included, the real figure was **47.6%**. A panel reporting 99% recommends nothing. The flattering error is also the one that makes the dashboard useless.

**Write payback breaks even at 1.25x, not 1.0x.** A write costs about 1.25x base and a read about 0.1x, so a write needs roughly 1.25 reads to pay for itself. A series sitting at 1.10x lost money while looking fine against a 1.0 line. The dashboards draw break-even at 1.25 and use a log axis, because the measured spread ran from 0.73x to 21x, on a linear axis every sub-break-even series collapses onto the floor, which is the end of the range the panel exists to surface. On this project's data the aggregate write payback was a healthy 3.60x while one model sat at 0.73x, a loss entirely invisible in the total.

**Coverage is not adoption.** Coverage is the share of commits carrying a valid trailer of any kind, including `undetermined`. A repository at 100% coverage may have had no agent involvement whatsoever. Adoption is the share carrying `status=assisted`. These get confused constantly. Coverage says the instrumentation is working; adoption says the agents are being used.

**A fix rate is not a defect rate.** The rework panel compares how often assisted and unassisted commits are fixes, but `purpose` comes from Conventional Commit prefixes and path heuristics, so it measures what commits were *labelled*, not what actually broke. It's a rework proxy. Useful, and not the same thing. Worth knowing: banding that panel by change size reversed its answer. Unbanded it read 28.4% assisted against 33.3% unassisted, which looks favourable; split by size it flips in the middle band to 42.1% against 24.7% at 50–300 lines. The aggregate was a size-mix artefact.

**And the productivity section reports a difference, not a gain.** Selection bias between assisted and unassisted commits is uncontrolled, and it is not fixable with better maths.

**This is my logic. Tell me where it's wrong.** I'd genuinely rather find out that the 1.25x break-even is off, or that my selection-bias argument has a fix I haven't seen, than keep shipping panels built on it. The metric definitions are the part of this project I'm least confident in and most interested in criticism of.

## How it's built

Two independent components. **You can use the first without ever touching the second.**

**`dun`: a Go CLI that runs on the developer's machine.** Sixteen top-level commands, but the loop is short:

```bash
cd your-repo
dun init          # installs prepare-commit-msg, commit-msg, pre-push hooks
git commit ...    # trailer gets stamped automatically
dun status        # coverage + method mix for recent commits
dun report        # self-contained HTML report, opens in any browser
```

`dun init` is once per repository, and it chains to any hook you already have rather than clobbering it. Instrumentation is always explicit. There is no flag to enrol every repository you've ever used an agent in, because that set includes client work, throwaway experiments, and clones of other people's projects. Stamping attribution trailers into those is a disclosure decision that belongs to you, one repo at a time.

Attribution comes from session transcripts the agents already write for their own purposes, `~/.claude/projects/**/*.jsonl` for Claude Code, `~/.codex/sessions/**/*.jsonl` for Codex, a local SQLite store for Antigravity. Matching is by content hash, never by commit SHA: a commit doesn't exist yet when the observation is recorded, and may later be amended, rebased or squashed. Hashing what changed rather than where it landed keeps attribution correct across all three.

`dun sync` is the only command that transmits anything. Worth saying loudly: **`dun report` renders a self-contained HTML file with no server and no network**. The entire dashboard layer below is optional.

**The DevLake layer: docker compose, and optional.** One script builds the stack, a second imports the dashboards; they're separate because step 2 is re-run on every whodunit release and step 1 isn't.

Seven Grafana dashboards, 140 panels: Adoption, Cost & Efficiency, AI Impact on Delivery, Executive Summary, AI Productivity Funnel, Agent Activity Hours, and the original AI attribution one.

### Why DevLake, when nothing here uses DevLake

This is the design decision I get asked about most, and the honest answer isn't "it's a nice dashboard tool".

**Nothing here uses DevLake's own collectors.** whodunit's data comes from git and the local journal, not from GitHub or Jira. A bare Postgres and Grafana would serve the same panels.

DevLake is the target because a team that already runs it should be able to add whodunit without standing up a second stack. That only works if the tables coexist with theirs, which is why every table is prefixed `whodunit_` and nothing writes to DevLake's domain tables. Their schema shifts between minor versions, and writing into it would make every DevLake upgrade a potential data-loss event.

That's it. Coexistence with a stack the client already has. The upside is real: one dashboard, `whodunit-dora.json`, joins attribution data to DevLake's own delivery metrics, which is how Q10 gets answered against real deployment data rather than commit timestamps.

## Privacy, stated plainly

I'd rather undersell this than oversell it, because a privacy page that omits the awkward part is worse than none: a reader who finds it later has reason to distrust everything else on the page.

**Collection makes no network calls.** The hooks, the daemon and `dun ingest` read git and local transcripts and write a local SQLite journal. The one exception is opt-out and unrelated to collection: `dun` checks once a day for a newer release, on bare `dun` only, never from a hook, disable with `dun config set version_check off`, or `DUN_NO_VERSION_CHECK` / `DO_NOT_TRACK`.

**The journal schema has no field that could hold prompt text, message content, file contents, hostnames or remote URLs.** This is a structural guarantee rather than a filtering step. There is nowhere to put them. Specific things considered and rejected: Claude Code's `toolUseResult` as a whole (it carries stdout, stderr, and the before/after text of every edit), Codex's `agent_message.message` and `unified_diff`, Antigravity's `CodeContent` and `ReplacementContent`. MCP tool-call arguments and web-search queries are countable but never stored, because they carry user intent verbatim.

**Now the part that does identify someone.** Two things:

**File paths.** The journal stores which files an agent edited. That reveals what someone worked on, not merely how much.

**A contributor email.** `whodunit_repos` stores the git committer email for the repository, the same address already in every commit you author. A shared database needs it to attribute anything to anyone.

Both stay local until you run `dun sync`, and `dun sync --dry-run` prints the exact payload before any target is contacted. Six tables are sent, enumerated in full in the docs so nobody has to take it on trust. Everything lives under `~/.whodunit`, owner-only, `0700` directories, `0600` files, repaired on open if they were ever created more permissively. Nothing is ever written into your repositories.

The repository id is its root commit SHA, stable across clones and machines, and identifying the repository without revealing its name or remote.

## The takeaway

If you're being asked what your AI tooling bought you, don't go looking for the dashboard that produces the number. **Separate the questions evidence can answer from the ones it can't, and be willing to say which is which in the meeting.**

Of the eleven, most are answerable from commits and local transcripts. Acceptance rate, model mix, autonomy, churn, purpose, coverage, per-person spread, those are real measurements with real denominators. Cycle time is answerable if your delivery data is wired up. Whether your developers understand compaction is not answerable, and neither is the productivity percentage: assisted and unassisted work are self-selected, and no amount of arithmetic fixes that.

A dashboard that shows you a productivity gain from this data has made a decision you didn't see. I'd rather show the difference, print the denominators, and let you argue with it.

Which is the actual ask here. **The metric logic is the part I want criticised**, the 1.25x break-even, putting cache writes in the denominator, banding the fix rate by change size, refusing to multiply the funnel stages. If one of those is wrong, or there's a defensible way to control for selection bias I've dismissed too fast, I want to know.

Code: [github.com/navjyotnishant/whodunit](https://github.com/navjyotnishant/whodunit)
Docs: [navjyotnishant.github.io/whodunit](https://navjyotnishant.github.io/whodunit/)

Issues and arguments both welcome.
