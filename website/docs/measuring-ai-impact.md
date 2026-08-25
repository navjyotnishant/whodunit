---
id: measuring-ai-impact
title: How this measures AI's effect
sidebar_label: Measuring AI's effect
sidebar_position: 2
description: The formula, what each term resolves to, and what the number deliberately does not claim.
---

# How this measures AI's effect

Every vendor in this space publishes a number. Almost none publishes how it
was computed, or what else changed in the window it was measured over.

This page is the method. It is deliberately checkable: each term below
names the field it resolves to, and each exclusion says why.

## The formula

```
Productivity gain = Δ(Output / Effort)
                    conditional on Work Type + Complexity + Quality
```

Read plainly: **how much effort it took to produce a comparable outcome,
before against after — holding the kind of work, its size, and whether it
came back as rework, as close to constant as the data allows.**

The conditioning is the part that matters. `Output / Effort` on its own
rises when the work gets easier, when the team grows, or when a quarter
happens to contain fewer incidents. Without the conditions it is not a
measurement of anything in particular.

## What each term resolves to

| Term | Resolves to | Where it comes from |
|---|---|---|
| **Output** | Work items completed | `issues.resolution_date` |
| **Effort** | **Cycle time** — not hours | Derived from issue open to close |
| **Work Type** | Issue type, commit purpose | `issues.type`, the trailer's `purpose` |
| **Complexity** | **Files touched**, banded | Observed from the diff |
| **Quality** | Fix-follow-on rate, revert rate | Derived from commit history |

### Effort is cycle time, and hours are deliberately unavailable

The natural denominator is engineering hours. It is not used, and adding it
would make everything else less trustworthy.

Hours are estimated. On the installation this framework was built against,
`time_spent_minutes` was populated on **0 of 473 issues** — not sparsely,
not partially: never. Asking teams to start logging them would import the
self-report problem, and a self-reported denominator moves with what people
think is expected of them.

Cycle time is worse in one way — it includes waiting, review and weekends —
and better in the way that decides it: nobody types it in.

### Complexity is files touched, and story points are ruled out

Story points look like the obvious complexity control and are the one input
that would actively corrupt this.

Two reasons, and the second is decisive:

1. **Coverage.** 11 of 473 issues carried one — 2.3%.
2. **The unit moves.** Teams lower their points once AI is in use. The same
   task that was a 5 becomes a 3, because everyone knows the agent will
   help.

That second one makes `Output / story_points` **circular**: the denominator
shrinks *because* of AI, so the ratio rises *because* of AI, whether or not
anything got faster. It is Goodhart's law arriving through the denominator,
and it produces a flattering number that survives scrutiny only until
somebody asks how points were assigned.

Complexity therefore uses properties nobody estimates — files touched,
distinct subsystems, whether a change crosses a module boundary. Imperfect,
and they cannot deflate.

## How strong is any given number

Not every comparison is worth the same. A figure without its design is not
interpretable, so every panel says which rung it stands on.

| | Design | Why it is stronger or weaker |
|---|---|---|
| 1 | **Randomised assignment** | Tasks assigned to agent or not at random. The only design that earns a causal claim. Out of reach for almost everyone. |
| 2 | **Matched historical pairs** | For each assisted item, compare against comparable unassisted ones. **This is what whodunit implements.** |
| 3 | **Cross-period** | Before against after adoption. Valid only with a baseline that genuinely predates AI use. |
| 4 | **Cross-sectional** | Assisted against unassisted in the same window. Weakest: the groups are self-selected. |

Rung 3 is unavailable to most installations for a reason worth knowing:
attribution begins when whodunit is installed, not when AI use began. A
"before" window drawn from the period before installation is measuring
*instrumentation*, not adoption. That is why `dun baseline capture` demands
an explicit `--since` and `--until` rather than defaulting to something
convenient — you are being asked to name a period you know was agent-free,
because the tool cannot work it out for you.

## What it looks like when it refuses

A methodology that only shows its successes is not one you can check. Here
is the matched-pair comparison on the installation it was designed against,
grouped by purpose and file-count band:

```
purpose  band            assisted  unassisted
feature  small (1-2)           14          23
feature  medium (3-8)          64          12
feature  large (9+)            17           2
fix      small (1-2)           25          21
fix      medium (3-8)          29           8
fix      large (9+)             6           0
```

**Every comparable cell is below the confidence floor on the unassisted
side.** The honest output is `n/a`, and that is what the panel shows.

The distribution is itself the finding. Unassisted work clusters in small
single-file changes; assisted work in medium and large ones. That is the
concrete form of *people reach for an agent on some kinds of work and not
others* — visible in the counts rather than asserted in a footnote.

## What this does not claim

**It does not say AI caused anything.**

Even with clean matching, the window contains everything else that changed
in it: who was on the team, what else shipped, how mature the product was,
which quarter it was. No measurement separates those without randomised
assignment.

So the framework reports **effort per comparable outcome**, and names what
it could not hold constant. A reader who wants "AI made us X% faster" is
asking for a number this data cannot support, and any tool that hands it to
them is not being more sophisticated — it is declining to say so.

Also out of scope, and stated rather than quietly absent:

- **Engineering hours.** See above.
- **Story points as complexity.** See above.
- **Developer experience.** How the work *feels* is real and this cannot
  see it. Surveys are the right instrument and whodunit is not one.
- **A single productivity score.** Speed, output and quality are reported
  side by side and never collapsed. A throughput gain arriving with more
  rework is deferred cost, and one number would hide exactly that.

## Why this is worth the trouble

The comparison is not against perfection. It is against what is currently
published:

- **+113% PRs per engineer**, from an observational cross-section with no
  methodology section, no confidence intervals and no confounder controls.
- **33% faster PR cycle times**, correlating adoption intensity against
  DORA, with no discussion of causality.
- **1.7× more issues per PR in AI-written code** — pointing the other way,
  and equally unpublished as to method.

None of those is dishonest. Each is a real correlation somebody measured.
What none of them states is what else moved at the same time, which is the
only thing that decides whether the number means what it appears to.

A stated method that returns `n/a` is more useful than a confident number
whose derivation nobody will show you.
