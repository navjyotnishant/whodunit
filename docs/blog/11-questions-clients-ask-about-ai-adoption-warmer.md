---
title: "Before You Claim AI Made Your Team Faster, Measure It"
published: false
description: "Eleven questions clients ask about AI adoption, the evidence whodunit can use, and the one productivity number I do not think we should fake."
tags: ai, metrics, opensource, devops
---

Every few months, I find myself in some version of the same conversation.

A team has been using AI coding tools for a while. The excitement has cooled into a more practical question: what did we actually get from this?

It is a fair question. Leaders have bought licenses, teams have changed habits, and somebody eventually has to explain whether any of it helped. The awkward part is that the usual answers do not feel very satisfying. Seat counts say who had access. Surveys say how people felt. Vendor dashboards say a tool was opened.

None of that tells you what happened in the codebase.

That is why I built [whodunit](https://github.com/navjyotnishant/whodunit). It is a small open-source tool for answering AI adoption questions from evidence that already exists: git commits, local agent logs, and, if you want the shared view, delivery data from DevLake.

The goal is not to make AI look good. It is to make the claims harder to misuse.

## The questions people actually ask

These are the questions I keep hearing from clients and engineering leaders.

1. Is my team actually adopting AI, and is adoption spread evenly?
2. Can you show me the productivity improvement?
3. Are people using AI well, or just chatting with it?
4. Is AI helping around the edges, or writing most of the code?
5. How much AI-written code is accepted?
6. Where is AI being used: features, fixes, tests, docs?
7. How much autonomy are developers giving the agent?
8. Can we see churn for AI-written code?
9. Which models are being used most?
10. Did PR cycle time improve?
11. How long did adoption take?

I like this list because it is honest. It is not a polished strategy deck. It is what people ask when they are trying to make sense of a real investment.

Some of these questions are answerable. Some are partly answerable. One of them, the productivity percentage, is the one I am most careful with.

## The small thing whodunit adds

whodunit adds a plain git trailer to commits:

```text
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

There is no magic in the format. That is the point. It is just git metadata, readable by any tool that can read a commit message.

The trailer records whether a commit was AI-assisted, which agent was involved, which model was reported, and how strong the evidence is. The strongest method, `intersected`, means text the agent produced survived into what was staged.

When the evidence is missing, whodunit records that too. It uses `undetermined` instead of quietly deciding "no AI was used." That distinction matters more than it sounds. A missing signal is not a zero. It is just missing.

## The one-screen answer

![Whodunit executive summary showing adoption, coverage, acceptance, active sessions, and delivery comparison](https://navjyotnishant.github.io/whodunit/img/dashboards/executive-summary.png)

The executive dashboard is the page for the person who will not open the rest. It shows assisted commits, coverage, acceptance rate, active sessions, and delivery comparison in one place.

The most important number on that page is not the flashiest one. It is coverage.

Coverage tells you how much of the commit history has a valid attribution trailer. Adoption tells you how much of that covered history was assisted. Those are different claims, and mixing them up is where bad AI metrics start.

A team can have high coverage and low adoption. That is a valid finding. It means the instrumentation is working and the evidence says AI is not showing up much in committed work.

A team can also have low coverage and a beautiful adoption number. That one should make you pause.

## Adoption is a spread, not a slogan

![Whodunit adoption dashboard showing contributor spread and committed work by contributor](https://navjyotnishant.github.io/whodunit/img/dashboards/adoption.png?v=2)

When someone asks "is the team adopting this?", the team average is not enough.

I want to know whether adoption is spread across the group or carried by two people. I want to know who has no assisted commits at all. I want to know whether the data covers enough of the repo to trust the answer.

That is why the adoption view shows contributor spread beside coverage. The contributor identity comes from the git committer email that is already present in commits. whodunit is not watching developers. It is making existing commit metadata easier to read.

There is one catch worth saying gently but clearly: the clock starts when instrumentation starts.

If your team used AI for six months before installing hooks, those six months are not magically recoverable. whodunit can tell you what happened after the trailers started landing. For a real before-and-after story, capture a baseline before the rollout with `dun baseline capture`.

Miss that window and you can still learn a lot. You just have to be honest about where the measurement begins.

## "Are they using it well?" is only partly measurable

![Whodunit cost and efficiency dashboard showing model mix, tokens, cache behavior, and compaction](https://navjyotnishant.github.io/whodunit/img/dashboards/cost-efficiency.png)

This is the question that can get uncomfortable fast.

People ask whether developers know compaction, whether they choose the right model, whether they use skills and sub-agents, whether they turn everyday work into good AI workflows. I understand why they ask. Training budgets depend on it.

But I do not think a dashboard should pretend to read competence from a transcript.

What whodunit can show is session shape. Did a session only involve conversation? Did it call tools? Did it edit files? Did it use many tools or MCP calls? Which models were used? Did the session compact?

That gives you a useful proxy for "just chatting" versus "using the agent to do work." It does not tell you whether a developer is skilled with AI. That still needs human context: pairing, coaching, code review, and conversation.

I would rather leave that boundary visible than build a confidence score no one should trust.

## Where AI shows up in the work

![Whodunit productivity funnel showing adoption, engagement, assisted work, and later stages that need stronger evidence](https://navjyotnishant.github.io/whodunit/img/dashboards/productivity-funnel.png)

For questions like "is AI being used for features, tests, docs, or fixes?", whodunit leans on commit prefixes and path heuristics.

That is useful, but only if your repository gives the tool something decent to work with. If your team uses Conventional Commits, a `feat:` commit is a pretty good signal. If your team does not, the feature panel will look empty even when people are shipping features all week.

So I treat purpose as a label, not an observation.

This is a recurring theme in the project: the chart should say what it knows, and just as importantly, what it does not know.

## Autonomy changes the shape of a session

Autonomy was more interesting than I expected.

Different tools have different permission vocabularies. Codex may report modes like `never` and `on-request`. Claude Code may report `acceptEdits`, `default`, or `auto`. whodunit keeps those names instead of pretending they all map neatly onto the same ladder.

The useful question is not only "how often did people grant autonomy?" It is "what happened once they did?"

On my data, high-autonomy sessions were fewer, but much denser. Tool calls per session captured that better than raw session count. Still, I would be careful with the interpretation. A mode that allows more actions will naturally produce more actions. Tool calls are activity, not delivered value.

## The productivity number everyone wants

![Whodunit delivery impact dashboard showing assisted versus other delivery metrics and the productivity caveat](https://navjyotnishant.github.io/whodunit/img/dashboards/ai-impact-on-delivery.png)

This is the question that started the whole thing:

> Show me the productivity improvement after AI adoption.

I wish that number were easier to produce honestly. It would make the meeting shorter.

The problem is that assisted and unassisted work are not random groups. Developers choose when to use an agent. They may use it for larger changes, repetitive migrations, unfamiliar code, tests, or work they already know how to delegate.

So if assisted commits are larger, what does that prove?

Maybe AI helped people take on bigger work. Maybe it encouraged bigger diffs. Maybe people simply used it on a different kind of task. The same chart supports all three stories.

That is why whodunit shows comparisons, not productivity gains. It can show change size, churn, acceptance, purpose, adoption, and cycle-time differences when delivery data is wired up. It should not turn those into a percentage and call it ROI.

A baseline helps. If you capture a pre-adoption window before installing hooks, `dun delta` can compare that period with a later one. That is much stronger than comparing assisted and unassisted commits inside the same period.

It is still observational. Teams change, codebases change, projects change. The baseline gives you a better conversation, not a laboratory experiment.

That may sound unsatisfying. In practice, I think it is a relief. It lets you say, "Here is what moved, here is what did not, and here is what we cannot honestly claim."

## The mistakes I am trying to avoid

The biggest metric mistakes are usually flattering.

**Empty is not zero.** If an agent does not report token usage, showing zero makes it look free. If a commit has no attribution evidence, treating it as "not AI" makes adoption look cleaner than the evidence allows.

**Tokens are not dollars.** A transcript does not know whether someone is using a subscription, an enterprise contract, or API billing. whodunit reports tokens and cache behavior. It does not invent a price.

**Cache writes count.** Cache read ratio should include uncached input, cache writes, and cache reads. Leaving writes out made one of my early analyses look much better than it was.

**A fix rate is not a defect rate.** Fix-labelled commits are a rework proxy. They are useful, but they do not prove AI caused or prevented defects.

**A difference is not a gain.** This is the one I keep coming back to. If the groups are self-selected, the comparison can be useful without being causal.

None of this makes the tool less useful. It makes it safer to use.

## How it is built

There are two pieces.

![whodunit solution architecture showing local git and agent evidence flowing through dun into trailers, a local journal, optional local reports, and optional DevLake/Grafana dashboards](https://navjyotnishant.github.io/whodunit/img/architecture/whodunit-solution-architecture.png)

At a high level, whodunit keeps the sensitive work local. The developer machine reads git and local agent logs, stamps attribution into commits, and keeps a local journal. A team only gets the shared dashboards if someone chooses to sync the `whodunit_*` tables into the optional DevLake layer.

The first is `dun`, a Go CLI that runs locally:

```bash
cd your-repo
dun init          # installs the git hooks
git commit ...    # the trailer is stamped automatically
dun status        # coverage and method mix
dun report        # local HTML report
```

Collection is local. The hooks, daemon, and ingest command read git plus local agent transcripts and write a local SQLite journal. `dun report` renders a self-contained HTML file without a server or network call.

The second piece is optional: a DevLake and Grafana layer for teams that want a shared view. whodunit writes to its own `whodunit_*` tables and leaves DevLake's domain tables alone. If DevLake is already where your delivery metrics live, this lets attribution sit beside them instead of creating another stack.

The supported adapters today are Claude Code, Codex CLI, and `agy` from Antigravity. Each agent records different things, and whodunit keeps those differences visible instead of smoothing them away.

## Privacy, plainly

The collection path makes no network calls.

There are two pieces of identifying information worth naming:

- File paths, because they reveal what someone worked on.
- Committer email, because a shared dashboard needs to attribute work to contributors.

Both already exist in or around the development workflow. Both stay local unless you configure sync.

The journal has no field for prompt text, message content, file contents, hostnames, or remote URLs. That is not a filtering promise. It is a schema choice. There is nowhere for those values to go.

## Where I landed

I built whodunit because I wanted a better answer in the room.

Not a louder answer. Not a prettier one. A more honest one.

Most of the eleven questions can be answered from commits and local transcripts. Adoption spread, model mix, acceptance, autonomy, churn, purpose, and coverage are all measurable with real denominators. Cycle time can be measured when delivery data is connected.

But whether a developer truly understands compaction or model choice is not in the data. And the clean productivity percentage people want is not there unless you are willing to make assumptions quietly.

I am not willing to make them quietly.

So the tool shows the difference, prints the denominator, and leaves room for argument. That is the part I care about most. If the metric logic is wrong, I want to know. If the 1.25x cache write break-even is off, tell me. If there is a better way to handle selection bias, I would genuinely like to hear it.

The point is not to win the AI adoption story.

The point is to make the story sturdy enough to trust.

Code: [github.com/navjyotnishant/whodunit](https://github.com/navjyotnishant/whodunit)  
Docs: [navjyotnishant.github.io/whodunit](https://navjyotnishant.github.io/whodunit/)
