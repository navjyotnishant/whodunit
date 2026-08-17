---
title: "The AI Adoption Dashboard That Refuses to Lie"
published: false
description: "Clients keep asking for an AI productivity number. whodunit answers the questions the evidence can support, and refuses the one it cannot."
tags: ai, metrics, opensource, devops
---

Six months after a company buys AI coding tools, the same meeting happens.

Finance wants to know what changed. Engineering leadership wants proof that adoption is real. Someone has a vendor dashboard open. Someone else has a survey. The numbers look tidy enough for a slide, but nobody in the room fully trusts them.

That is the moment this project is built for.

Over the last couple of years, clients have asked me the same set of questions about AI adoption. Not abstract strategy questions. Practical ones:

- Who is using it?
- What are they using it for?
- Is it helping delivery?
- Are people just chatting with it?
- What did we actually get for the money?

The uncomfortable truth is that some of those questions can be answered from evidence. Some can only be partially answered. And one of them, the most tempting one, cannot be answered honestly from the data most teams have.

That question is:

> Show me the productivity improvement after AI adoption.

[whodunit](https://github.com/navjyotnishant/whodunit) exists because I would rather build the dashboard that says "we cannot prove that" than ship the dashboard that makes up a percentage.

## The Problem With Most AI Adoption Dashboards

The usual evidence is weak in two different ways.

Surveys tell you how people feel about tools they were told to adopt. Seat dashboards tell you who has access. Chat logs tell you a tool was opened. None of those prove that AI-generated work reached the codebase, survived human editing, or changed delivery.

The source of truth I care about is more boring and more useful: commits.

Every commit can carry a plain git trailer:

```text
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

That trailer says which agent touched the change, how strong the evidence is, and roughly how much of the committed diff came from the agent. It is readable by anything that can read a commit message. It travels with the repository. It cannot be quietly added later without rewriting history.

The important field is `method`.

At the strongest level, `intersected`, whodunit has evidence that text produced by the agent is the text that ended up staged. At weaker levels, it says so. When the evidence is missing, it records `undetermined` instead of pretending that "no evidence" means "no AI."

That one distinction, between absence and zero, is the whole philosophy of the project.

## The 11 Questions Clients Actually Ask

These are the questions I keep hearing, in the words clients actually use.

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

The honest answer is not one dashboard. It is a separation exercise:

- What can commits prove?
- What can local agent transcripts prove?
- What requires delivery data from another system?
- What sounds measurable but is actually inference dressed up as certainty?

whodunit answers the first three where it can, and puts a wall around the fourth.

## What You Can See Right Away

![Whodunit Executive Summary](https://navjyotnishant.github.io/whodunit/img/dashboards/executive-summary.png)

*Every screenshot here comes from a demo dataset. Repository names, contributors and projects are anonymised, and the issue-to-commit links are seeded so the cycle-time panels have enough data on both sides to render. The numbers show what the panels do, not what any team achieved.*

The executive view is for the person who will never open the other dashboards: assisted commits, attribution coverage, acceptance rate, active sessions, trend lines, and cycle-time comparison in one place.

It does not try to win the argument by making the number bigger. It tries to make the denominator visible.

Coverage and adoption are deliberately separate:

- **Coverage** means commits carry a valid AI attribution trailer.
- **Adoption** means the trailer says `status=assisted`.

A repository can have perfect coverage and no observed AI involvement. That is not a contradiction. It means the instrumentation is working and the evidence says what it says.

## 1. Is AI Actually Being Adopted?

![Whodunit Adoption](https://navjyotnishant.github.io/whodunit/img/dashboards/adoption.png)

For adoption, the useful view is not a single percentage. It is the spread.

The adoption dashboard shows activity by contributor and committed work by contributor. Identity comes from the git committer email already present in commits; whodunit is making existing commit metadata queryable, not inventing a new surveillance layer.

This answers the practical management question:

> Is adoption evenly distributed, or are two people carrying the whole number?

You can see who has assisted commits, who has none, and whether the team-wide number is meaningful or resting on a thin slice of people.

For adoption over time, whodunit can show a per-agent curve. An agent gets credit for a commit when it recorded activity in the repository within the preceding 24 hours. That is a heuristic, and the dashboard treats it like one. If two agents touched the same commit, both series may move; the lines are not supposed to sum to the total.

The harder part is historical adoption. If you install instrumentation today, everything before today is dark. That is why `dun baseline capture` exists: the real before-and-after window closes as soon as hooks start stamping commits.

## 2. Are People Using AI Well, Or Just Chatting?

This is the most politically loaded question because it sounds like a competence ranking.

whodunit does not know whether a developer understands compaction. It does not know whether they chose the best model. It does not know whether they wrote good skill files, configured sub-agents well, or built a thoughtful workflow.

I do not want a panel called "AI maturity" that is really a pile of proxies and vibes.

What whodunit can measure is session shape:

- Was the session conversation-only?
- Did it call tools?
- Did it edit files?
- Did it use deeper agentic behavior such as multiple tools or MCP calls?
- Which models were active?
- What did the token mix look like?
- Did sessions compact?

That gives a decent answer to the "just chatting" part. It does not answer the "does this person know what they are doing" part, and the dashboard should not pretend otherwise.

Model usage is more direct. The cost and efficiency dashboard shows token mix by model and separates cache reads, cache writes, and uncached input where agents report them. It reports tokens, not dollars, because a transcript does not know whether a user is on a subscription, an API bill, a team contract, or something else entirely.

If you need currency, you can multiply by your own contract. The tool should not guess.

## 3. Where Is AI Being Used?

"Where is AI helping?" sounds simple until you ask what "where" means.

whodunit uses purpose labels from Conventional Commit prefixes and path heuristics. That means the dashboard can distinguish things like feature work, tests, docs, and fixes when the repository gives it enough signal.

It also means the panel has a real limit:

> Purpose is a label, not an observation.

If a team does not use Conventional Commits, `feat:` will not magically appear. The dashboard will show little feature work even if the team shipped features all week. That is not a hidden AI insight. It is an input-quality problem.

This is one of the places where I would rather have a less impressive dashboard and a more honest caption.

## 4. Is AI Supplementing Work, Or Writing The Whole Thing?

The trailer's `ratio` field exists for this question.

If a commit has `ratio=0.62`, whodunit is saying that roughly 62% of the change is attributable to the agent under the recorded method. The ratio is only as strong as the method beside it:

- `intersected` means produced agent text survived into the staged diff.
- `observed` means the agent touched relevant files, but the text changed afterward.
- `inferred` and `declared` are weaker.
- `undetermined` means the evidence is not there.

That lets you distinguish "AI helped with a few lines" from "AI produced most of this change" without pretending every assisted commit is the same.

It also keeps the argument anchored in code that landed, not prompts that sounded productive.

## 5. What Autonomy Are People Granting?

The autonomy question turned out to be more interesting than I expected.

Different agents use different permission vocabularies. Codex may report modes such as `never` or `on-request`; Claude Code may report modes such as `acceptEdits`, `auto`, or `default`. whodunit does not flatten those into one fake enum, because that would claim equivalence the data cannot support.

Instead, the dashboard shows how much activity happened under each agent's own mode.

The useful metric is tool calls per session. Sessions and total calls can move in opposite directions; a mode used rarely can produce a huge amount of activity once granted. Tool calls per session captures that shape better than a raw count.

The caveat matters: tool calls are not shipped work. A permissive mode permits more actions by design. The chart shows activity under autonomy, not value created by autonomy.

## 6. What Code Did Humans Accept?

Acceptance rate is one of the cleanest things the tool can report, when the agent records enough information.

Claude Code and Codex expose accepted and failed edit outcomes differently. `agy` can attribute edits, but does not expose a true accept/reject signal, so it cannot honestly contribute to the same acceptance-rate panel.

That is a boring implementation detail with a big dashboard consequence: agents without the signal should not dilute the denominator.

The useful view is not just "overall acceptance." It is acceptance by tool and by agent, always with the count beside the percentage. A 90% rate over 10 calls and a 60% rate over 10,000 calls are not the same finding.

There is also a stronger signal where available: whether a human edited the agent's output before it landed. That is closer to "how much survived" than a binary accept/reject label, but only agents that report enough edit detail can support it.

## 7. Did Delivery Change?

![Whodunit AI Impact on Delivery](https://navjyotnishant.github.io/whodunit/img/dashboards/ai-impact-on-delivery.png)

This is where the conversation usually turns from interesting to dangerous.

whodunit can show:

- AI-written lines added and removed over time.
- Assisted versus unassisted change size.
- Rework proxies such as fix-labelled commits.
- PR cycle time when DevLake delivery data is connected.
- DORA-style delivery correlations when DevLake is configured correctly.

Those are useful. They are not a productivity percentage.

The difference is selection bias.

Assisted and unassisted commits in the same period are not two randomized groups doing the same work. Developers choose when to use an agent. They may reach for it on larger changes, repetitive migrations, unfamiliar code, test generation, or tasks they already know how to delegate.

So if assisted commits are larger, what did we learn?

Maybe AI helped people finish bigger tasks. Maybe it encouraged larger diffs. Maybe it was used on work that was already different. All three are consistent with the same chart.

That is why the dashboard has a panel called "Why this is not a productivity percentage."

It does not color positive deltas green, because "larger commits" is not automatically "better work." It does not call an assisted/unassisted comparison a gain, because the comparison is not causal.

This is the part clients least want to hear and most need to hear.

## The Metric Traps That Make AI Look Better Than It Is

The dangerous errors are usually flattering.

### Empty Is Not Zero

A missing value is not a measured zero.

If an agent does not report tokens, showing `0` makes it look free. If a commit has no attribution evidence, treating it as "not AI" makes adoption look lower than it may be. If a dashboard panel has no delivery data, displaying a tidy zero turns missing instrumentation into a performance claim.

whodunit records absence as absence. That makes some panels less satisfying, but it keeps them from lying.

### Tokens Are Not Dollars

The cost dashboard reports tokens and cache behavior, not money.

Under a subscription, the marginal dollar cost of a token may be zero. Under API billing, it is not. A transcript does not know the commercial agreement behind it. Multiplying every token by a public API price would create a fictional cost model before the analysis even starts.

### Cache Writes Belong In The Denominator

Cache read ratio is easy to inflate.

The correct denominator includes uncached input, cache writes, and cache reads:

```text
cache_read / (uncached_input + cache_write + cache_read)
```

A cache write arrives uncached and costs more than base input. Leaving writes out makes the number look wildly better. On this project's own data, that mistake turned a real figure around 48% into something that looked like 99%.

A dashboard showing 99% says "nothing to fix." The corrected number says "look closer."

### Cache Write Payback Is Not 1.0x

A cache write has to be read enough times to pay for itself. With a write around 1.25x base and a read around 0.1x, break-even is roughly 1.25 reads, not 1.0.

That is why the dashboard marks 1.25x as the threshold. A model sitting at 1.10x can look healthy against the wrong line while still losing money.

### Rework Is Not Defect Rate

A fix-labelled commit is not proof that AI caused a defect. It is a proxy for rework, shaped by commit conventions and path heuristics.

Even that proxy can flip when you band by change size. In my own data, the aggregate looked favorable to assisted commits. Split by size, the middle band told a different story. The aggregate was partly a mix artifact.

This is why I keep coming back to denominators. Without them, every chart is an invitation to overread.

## How whodunit Works

There are two pieces, and you can use the first without the second.

### `dun`: The Local CLI

`dun` runs on the developer's machine.

```bash
cd your-repo
dun init          # install git hooks
git commit ...    # stamp the trailer automatically
dun status        # inspect coverage and method mix
dun report        # generate a self-contained local HTML report
```

`dun init` installs git hooks for one repository at a time and chains to any hooks already present. It does not enroll every repository on the machine. That is deliberate: adding AI-attribution trailers to commits is a disclosure decision, and disclosure belongs to the repository owner.

Attribution comes from local session stores the agents already write:

- Claude Code JSONL transcripts.
- Codex CLI JSONL rollout files.
- `agy` local SQLite conversations.

The journal does not store prompt text, message content, file contents, hostnames, or remote URLs. It stores the measurements needed for attribution: edited paths, counts, outcomes where available, model where reported, and line hashes.

`dun report` renders locally. No server. No network.

`dun sync` is the command that transmits data, and `dun sync --dry-run` prints the payload before sending it.

### The Optional DevLake Layer

The DevLake stack is for teams that want a shared view across people and repositories.

It is optional. whodunit's own data lives in `whodunit_*` tables and does not write into DevLake's domain tables. That keeps it from depending on DevLake's internal schema and lets existing DevLake users add AI attribution without standing up a second analytics stack.

The dashboards cover adoption, cost and efficiency, delivery impact, executive summary, productivity funnel, activity hours, and attribution detail. The DORA view can join whodunit data to DevLake delivery metrics when DevLake is configured with the right GitHub, project, and deployment settings.

That last phrase matters. DevLake can fail quietly. A misconfigured token, missing deployment pattern, or bad project mapping can produce empty panels that look like "nothing happened." whodunit's documentation calls those failure modes out because silent emptiness is another way metrics lie.

## Privacy, Without Handwaving

The collection path makes no network calls.

Hooks, the daemon, and `dun ingest` read git plus local agent transcripts and write a local journal under `~/.whodunit`. Data leaves only when a user configures a sync target and runs `dun sync` or pushes with sync enabled.

The awkward parts are stated plainly:

- File paths reveal what someone worked on.
- Committer email identifies the contributor, just like git already does.

Both are necessary for useful attribution. Both stay local unless the user syncs.

The schema has no place to store prompt text or file contents. That is stronger than filtering. It means the most sensitive payloads are not merely discarded; they are not representable in the journal.

## The Answer I Am Comfortable Giving

If a client asks "are people adopting AI tools?", I can answer.

If they ask "which agents and models are being used?", I can answer where the tools report it.

If they ask "is AI reaching committed code?", I can answer with evidence strength.

If they ask "where is it being used?", I can answer within the limits of commit labels and paths.

If they ask "did PR cycle time move?", I can answer when delivery data is connected and the sample is large enough.

If they ask "show me the productivity improvement," I will not give them a fake percentage.

The best version of this project is not the one that makes AI look good. It is the one that makes the evidence hard to misuse.

That is the real promise of whodunit: not certainty, but discipline. It separates what happened in the codebase from what we wish happened in the org chart. It prints the denominator. It leaves missing values empty. It refuses to convert correlation into causation because the slide would look better.

And if any of the metric logic is wrong, I want the argument. The cache break-even, the productivity refusal, the treatment of missing data, the decision to keep purpose as a label rather than an observation: those are the parts I most want challenged.

The goal is not to win the AI adoption narrative.

The goal is to stop lying to ourselves with prettier charts.

Code: [github.com/navjyotnishant/whodunit](https://github.com/navjyotnishant/whodunit)  
Docs: [navjyotnishant.github.io/whodunit](https://navjyotnishant.github.io/whodunit/)
