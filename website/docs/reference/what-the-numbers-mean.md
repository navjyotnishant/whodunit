---
id: what-the-numbers-mean
title: What the numbers mean
sidebar_label: What the numbers mean
---

# What the numbers mean

Every figure this tool reports has a definition that can be got wrong, and
several of them are wrong in a *flattering* direction — which is the
dangerous kind, because nobody questions a number that looks good.

## Empty is not zero

The rule the rest of the tool is built on.

**A missing measurement is recorded as absent, never as zero.** A `NULL`
token count means the agent does not report tokens. A blank latency panel
means nothing measured it. Neither means the work was free or
instantaneous.

The distinction matters because zero is a *value*. Averaged, charted or
summed, it makes a claim: that an agent cost nothing, that a session took
no time, that no AI was involved. Absence makes no claim at all, which is
the honest position when nothing was observed.

This is why `status=undetermined` exists instead of a "no AI" value, why
Antigravity is excluded from cost panels rather than shown at 0, and why
`model=` is omitted rather than emitted as `unknown`.

## Zero before you installed is not zero

A repository's commits are recorded from the moment `dun init` runs, but
attribution only begins when an agent's transcript is first matched against
a commit. Between those two points every commit is `undetermined` — which
means *no evidence*, and is correct.

The danger is what a chart does with that. Plotted as a rate, "no assisted
commits" becomes **0%**, and 0% reads as "no AI was used." It is the same
error as [empty is not zero](#empty-is-not-zero), committed by the renderer
rather than the schema, and it is the flattering direction: it manufactures
an adoption ramp from nothing.

Measured on one install, four repositories showed 0, 4, 26 and **76 days**
between first tracked commit and first attributed commit. That is up to two
and a half months of history a chart would have drawn as a flat zero line.

So the series is **absent before that boundary, not zero** — a gap, per
repository, because different repositories are instrumented on different
days. Commit volume, cycle time and revert rate stay plotted across it:
those are read from git and are just as true before instrumentation as
after. Only the attribution is unknown.

**A before-and-after comparison anchored in that window measures
instrumentation, not adoption.** This is why `dun baseline capture` takes
an explicit `--since` and `--until` rather than defaulting to a window
ending today: you are being asked to name a period you know was
agent-free, because the tool cannot work it out for you.

## Cost is in tokens, never currency

There is no dollar figure anywhere in this tool, and that is a decision
rather than a gap.

**Under a subscription the marginal cost of a token is zero.** Someone on a
fixed monthly plan spends the same whether a session burns 10k tokens or
10M. Multiplying their token count by an API rate would report money nobody
spent — not an imprecise figure, a categorically wrong one.

Nothing in any transcript says which billing model a user is on, so a
currency figure would require guessing at the *pricing model* before even
reaching the price. Prices also vary by contract and tier, change without
notice, and differ across the four token classes.

Tokens in and out, per model, per branch, are exact and need no
assumption. Anyone who needs money has their own contract and can multiply
correctly for their own billing.

## Cache writes count as uncached

The cache read ratio is:

```
cache_read / (uncached_input + cache_write + cache_read)
```

**Cache writes belong in the denominator.** A write arrives uncached and is
billed *above* base rate — roughly 1.25x. Leaving it out turns a real 48%
into a flattering 99%.

Measured on this project's own data, that error is not hypothetical: the
first version of the analysis reported a 99% hit rate and concluded caching
needed no attention. Recomputed with writes included, the real figure was
47.6%.

A panel reporting 99% recommends nothing. The flattering error is also the
one that makes the dashboard useless.

## Write payback breaks even at 1.25x, not 1.0x

Write amortisation is `cache_read / cache_write` — how many reads a write
earned before expiring.

A write costs about **1.25x** base and a read about **0.1x**, so a write
needs roughly 1.25 reads to pay for itself. A series sitting at 1.10x lost
money while looking fine against a 1.0 line.

The dashboards draw break-even at 1.25 for exactly this reason, and use a
log axis, because the measured spread runs from 0.73x to 21x — on a linear
axis every sub-break-even series collapses onto the floor, which is the end
of the range the panel exists to surface.

**Report it per model, never as one number.** Measured here, the aggregate
was a healthy 3.60x while one model sat at 0.73x — a loss, invisible in the
total.

## The productivity section reports a difference, not a gain

> The full method — the formula, what each term resolves to, and why hours
> and story points are excluded — is on
> [How this measures AI's effect](../measuring-ai-impact). This section is
> the short version.


The delivery dashboard compares assisted against unassisted commits. It
reports a **change-size difference** and refuses to call it a productivity
gain.

The reason is selection bias, and it is not resolvable by better maths:
people reach for an agent on some kinds of work and not others. A
throughput difference between the two groups may be measuring which work
was chosen rather than what the agent did.

Every such panel carries its denominators and its `n`. Where a cohort falls
below 20 commits either side, the figure is rendered as unavailable rather
than as a number — a rate over a handful of commits moves several points on
one or two of them.

## Coverage is not adoption

**Coverage** is the share of commits carrying a valid trailer of any kind,
including `undetermined`. A repository at 100% coverage may have had no
agent involvement whatsoever.

**Adoption** is the share carrying `status=assisted`.

They are different questions and the first is often mistaken for the
second. Coverage says the instrumentation is working; adoption says the
agents are being used.

## A fix rate is not a defect rate

The rework panel compares how often assisted and unassisted commits are
fixes. `purpose` is classified from Conventional Commit prefixes and path
heuristics, so it measures **what commits were labelled**, not what
actually broke.

It is a rework proxy. Useful, and not the same thing.
