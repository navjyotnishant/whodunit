---
id: the-trailer
title: Reading the trailer
sidebar_label: Reading the trailer
---

# Reading the trailer

```
AI-Attribution: v=1; status=assisted; method=intersected; agent=claude-code; agent_version=2.1.228; ratio=0.62; model=claude-opus-5; session=a3f9e21c
```

A plain git trailer. Readable by `git log`, by `git interpret-trailers`, and
by anything else that reads a commit message — the format is a
specification first and this tool's output second.

## The keys

### `v` — format version

First, so it is read before the values it qualifies.

It exists because `ratio=0.62` is well-formed under *any* definition of
ratio. Change the rule for computing it and every trailer already written
silently means something else, with no error to notice. Trailers live in
commit messages, so that ambiguity would be permanent: a migration can
rewrite a database column, nothing can rewrite pushed history.

A trailer with no `v` is version 1, permanently — that is every trailer
written before the key existed.

The version is bumped only when the **meaning** of a value changes, never
when a key is added. A parser that meets an unknown key keeps it and
re-emits it unchanged, so additions are backward compatible on their own.

### `status` — assisted, or no evidence

| Value | Meaning |
|---|---|
| `assisted` | There is evidence an agent contributed |
| `undetermined` | No evidence either way |

There is deliberately no value meaning "written without AI". See
[Empty is not zero](../reference/what-the-numbers-mean#empty-is-not-zero).

### `method` — how much to trust it

The confidence ladder, weakest to strongest:

| Method | What it means | Evidence |
|---|---|---|
| `undetermined` | No evidence either way | none |
| `declared` | Someone stated it | assertion |
| `inferred` | Derived from surrounding signals | circumstantial |
| `observed` | An agent edited these files in a recent session | file-level |
| `intersected` | The exact lines the agent produced were staged | line-level |

`intersected` is the only level that proves the agent's output survived
into the commit. `observed` means the agent touched the file, but the text
may have been rewritten before it was staged.

### `agent` / `agent_version`

Which tool, and which version of it. Read from the transcript, not
configured.

### `model`

Which model produced the work, where the agent reports one.

Taken from the **most recent** relevant session entry rather than the
first. A commit can contain edits from more than one model — a session that
escalated part-way, or two sessions touching the same files — and the turn
that finished the work is the one worth attributing.

Omitted entirely when no entry recorded one, rather than emitted as
`unknown`. A commit attributed by `declared` or `inferred` has no session
to read a model from.

### `ratio`

The fraction of the commit's changed lines that match lines the agent
produced.

The denominator is `lines added + lines removed` in the staged diff, and
the numerator counts **distinct** matching lines — a file that legitimately
repeats a line does not let one agent-written line claim several.

Omitted rather than guessed when the commit's line counts are unknown.

### `session`

An opaque token. It groups edits that happened together; it does not
identify a person, and it is not derived from anything that does.

## Checking coverage in CI

Local hooks are advisory — `--no-verify` bypasses them. The CI check is
what makes coverage real:

```yaml
- run: dun check --base "origin/${{ github.base_ref }}"
```

`dun check` fails if any commit since `--base` lacks a valid trailer. It
does **not** fail on `status=undetermined`, because that is a legitimate
verdict rather than a missing one.
