---
id: first-commit
title: Your first commit
sidebar_label: Your first commit
---

# Your first commit

## Instrument a repository

Instrumentation is per repository and always explicit. There is no flag to
enrol every repository you have ever used an agent in, and that is
deliberate: the set of repositories with agent transcripts includes client
work, throwaway experiments, and clones of other people's projects.
Stamping a commit with an attribution trailer is a disclosure decision, and
it belongs to you, one repository at a time.

```sh
cd your-repo
dun init
```

```text
installed prepare-commit-msg
installed commit-msg
installed pre-push

checking for AI agents
  agy            found             no sessions for this repository yet
  claude-code    not found
  codex          found             no sessions for this repository yet

  an agent stored somewhere else? point dun at it:
    dun config set agent.<name>.path <dir>

Publish attribution so it can be compared against what shipped? [y/N]
```

Three hooks. `prepare-commit-msg` and `commit-msg` write and validate the
trailer; `pre-push` publishes to a sync target if you configure one, and
does nothing if you do not.

`dun init` **chains to any hook you already have** — husky, pre-commit,
lefthook — rather than replacing it. That property is what makes automatic
hook repair safe later.

## Commit something

```sh
echo "package main" > main.go
git add main.go
git commit -m "feat: add main"
```

```text
feat: add main

AI-Attribution: v=1; status=undetermined; method=undetermined
```

That is the correct result for a file you typed yourself, and it is worth
pausing on, because it is the single rule the rest of the tool is built
around.

:::info `undetermined` does not mean "no AI was used"

It means **no evidence either way**. whodunit never asserts the absence of
AI involvement as a fact, because it cannot observe it: an agent that ran
in a way the tool could not see is indistinguishable from an agent that
never ran.

If a missing measurement were recorded as a negative finding, every gap in
collection would become a false claim about how the code was written — and
those claims would be permanent, sitting in commit messages nothing can
rewrite.

:::

## Check coverage

```sh
dun status
```

```text
commits examined:  1
coverage:          1/1 (100%)
method mix:
  undetermined     1   no evidence either way
```

**Coverage is not adoption.** It is the share of commits carrying a valid
trailer of any kind — including `undetermined`. A repository at 100%
coverage may have had no agent involvement at all. The method mix beneath
it is what says how much evidence there actually is.

## When an agent has been working

Once you have used a supported agent in this repository, the trailer fills
in:

```text
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=1.00; model=claude-opus-5; session=d1a8a52b
```

`method=intersected` is the strongest level: the exact lines the agent
produced are the lines that got staged. See
[Reading the trailer](the-trailer) for what each key means, and
[How attribution works](../reference/how-attribution-works) for how the
level is decided.

## What is instrumented

```sh
dun repos list        # what you have instrumented
dun repos candidates  # repositories with agent activity and no hooks
```

`dun repos candidates` only reports. Nothing enrols a repository except
you running `dun init` in it.
