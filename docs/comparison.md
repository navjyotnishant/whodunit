# whodunit and vendor usage APIs

Apache DevLake ships plugins that collect AI-coding usage directly from
vendors: `claude_code` reads Anthropic's organization admin API, `gh-copilot`
reads GitHub's, `q_dev` reads AWS's. If you are evaluating whodunit and one of
those is available to you, this is the honest comparison.

## The short version

A vendor usage API answers **"who is using the tool, and how much"**, for
everyone on the plan, without anyone installing anything.

whodunit answers **"which code came from an agent"**, for repositories where
someone chose to instrument it.

## What the vendor genuinely has that whodunit cannot

**Coverage without enrolment.** This is the real difference, and it is
structural. An org admin token reports on every seat at once. whodunit
requires each person to run `dun init` per repository and `dun sync`, so
anyone who has not is simply absent from the data — and absence is not
evidence of non-use. A vendor's denominator is "everyone with a seat"; ours is
"everyone who opted in."

**Seat and licence accounting.** Assigned seats, pending invites, seats going
unused. That is billing-side information, only the vendor has it, and it is
what most adoption reviews are actually asking about.

**Activity outside instrumented repositories.** Scratch directories, other
people's clones, work in repos nobody ran `dun init` on.

That is the complete list. Everything else below, whodunit either collects or
could.

## What whodunit has that a vendor API cannot

**Attribution to specific commits and lines.** A usage API knows a developer
accepted 40 edits on Tuesday. It does not know which of those became
`internal/journal/journal.go:112`. whodunit matches the text an agent produced
against the lines actually staged, so attribution attaches to commits and
travels with them in the commit message.

**Work that never shipped.** Files an agent touched that never reached a
commit; blocks it wrote and rewrote. Neither a commit-derived nor a
seat-derived metric can see either.

**Purpose classification.** Whether a commit was a feature, a fix, tests, or
config — so "80% AI-assisted" can be read against what kind of work it was.

**Before-and-after delivery comparison.** A pre-adoption baseline and the delta
from it, with revert rate reported next to throughput so a velocity gain
cannot be claimed without checking it is not deferred rework.

**No plan, no admin, no vendor.** No enterprise agreement, no organization, no
admin token, no account. It reads local files and git. That covers
individuals, small teams, contractors, and anyone whose employer has not
bought seats — none of whom appear in a vendor's admin API at all.

**Any agent, one schema.** The adapter interface is per-agent; a vendor API
covers that vendor. Cross-tool comparison on one set of definitions is
something no single vendor can offer.

## What whodunit could collect but does not yet

Listed because the distinction matters: these are engineering gaps, not
limits of the approach. The data is present in the session transcripts
whodunit already reads.

**Accept and reject rates.** A declined tool call is recorded — the transcript
contains an explicit "the user doesn't want to proceed with this tool use"
result, alongside edits that failed for other reasons. Both a rejection count
and a distinction between "declined" and "failed" are derivable. Not
implemented.

**Chat-side activity.** Message counts, conversation counts, tool diversity,
MCP connector usage, attachment counts. All present as record and block types.
Counting them requires no prompt text, so it is compatible with the
no-prompt-text rule; only content-derived signals are constrained, and those
are tracked separately.

**Per-user aggregation.** Already implemented. The sync architecture carries a
contributor per repository and the adoption dashboard groups by it. The limit
here is enrolment, not capability.

## Where they overlap

Both report lines written, sessions, activity over time, and per-person
breakdowns. If you have both, treat the vendor's numbers as the authority on
*usage across the org* and whodunit's as the authority on *what reached the
codebase*.

They will not reconcile, and are not meant to. A vendor counts every accepted
suggestion; whodunit counts what survived into a commit. The gap between them
is itself informative — roughly, the work that was tried and discarded.

## Practical guidance

| Situation | Use |
|---|---|
| Enterprise plan, admin access, want seat and adoption metrics | The vendor plugin |
| Want AI use tied to specific shipped code | whodunit |
| Want both | Both — the tables coexist in one database |
| No enterprise plan, mixed agents, or individual use | whodunit |

## A note on framing

It is tempting to claim whodunit collects "everything they do plus more." That
is close to true on the data, and false on the population: a vendor sees
everyone with a seat, whodunit sees everyone who opted in. Coverage is the
difference that matters, and it is the one thing a local collector cannot
engineer its way out of.

The accurate claim is narrower and stronger: whodunit is the only one of these
that ties AI assistance to the code that shipped, and the only one that works
without a vendor relationship.
