# whodunit

A git trailer standard for AI-attribution provenance, with an optional local
collector that fills it in automatically.

Yes, the name invites the surveillance joke. The suspect is the commit, not
the developer — `whodunit` answers "which agent touched this code," not
"who wrote this line." Nothing here reads prompts, keystrokes, or file
contents, and nothing ever leaves your machine unless you push it yourself
(see [Privacy](#privacy)).

## The trailer

Every commit gets a plain git trailer:

```
AI-Attribution: status=assisted; method=observed; agent=claude-code; agent_version=2.1.227; ratio=0.62; session=a3f9e21c
```

- **status** — `assisted` or `undetermined`. Absence of AI involvement is
  never asserted as fact; a commit with no evidence either way is
  `undetermined`, never silently treated as "no AI."
- **method** — how confident the attribution is, lowest to highest:
  `undetermined` → `declared` → `inferred` → `observed` → `intersected`.
  `intersected` means the exact text an agent produced is what ended up
  staged — the strongest evidence available.
- **agent / agent_version** — which tool, which version.
- **ratio** — fraction of the change attributable to the agent.
- **session** — an opaque token, not an identity.

Unknown keys are preserved, not dropped — the grammar is meant to outlive
any one implementation of it.

## Install

```sh
brew tap navjyotnishant/tap
brew install navjyotnishant/tap/dun
```

Or build from source:

```sh
go install github.com/navjyotnishant/whodunit/cmd/dun@latest
```

Or grab a binary from the [releases page](https://github.com/navjyotnishant/whodunit/releases) —
built with [`scripts/release.sh`](scripts/release.sh), not a third-party
release tool.

## Use

```sh
cd your-repo
dun init          # installs prepare-commit-msg + commit-msg hooks
git commit ...     # trailer gets stamped automatically
dun status         # coverage + method mix for recent commits
dun report         # self-contained HTML report, opens in any browser
```

`dun init` chains to any hook you already have installed — it never
clobbers existing `prepare-commit-msg`/`commit-msg` scripts.

### Commands

| Command | What it does |
|---|---|
| `dun baseline capture` | Snapshot delivery metrics **before** installing hooks — see below |
| `dun init` | Install the git hooks into the current repo |
| `dun status` | Trailer coverage and method mix for recent commits |
| `dun report` | Self-contained local HTML report — coverage, penetration, method mix, purpose distribution, per-commit detail |
| `dun delta` | Velocity and revert-rate comparison, before/after adoption — see below |
| `dun check --base <ref>` | Fail if any commit since `--base` lacks a valid trailer — the CI gate |
| `dun ingest [--since <time>]` | Read local agent session transcripts into the journal |
| `dun daemon run` | Foreground watcher: re-ingests continuously as sessions change |
| `dun journal show` / `dun journal purge` | Inspect or wipe the local journal |

### Capture a baseline first

If you ever want to answer "did this change how we ship?", run this
*before* `dun init`:

```sh
dun baseline capture
```

It records commit volume, median diff size, revert rate, commit cadence,
and purpose distribution over the last 90 days, as a dated immutable
snapshot. The pre-adoption window closes the moment hooks start stamping
trailers, and it cannot be recaptured afterwards.

PR throughput, cycle time, and change-failure rate can't be read from git,
so they're optional flags you supply from GitHub Insights or your CI
dashboard (`--prs-merged`, `--median-cycle-hours`, `--change-failure-rate`).
Anything you don't pass is omitted from the snapshot rather than recorded
as zero.

### Did it change how we ship?

```sh
dun delta
```

Reports two independent cuts, because either alone misleads:

- **Within-period** — assisted commits vs undetermined commits in the same
  window. Controls for calendar effects, but the two groups are
  self-selected: people reach for an agent on some kinds of work more than
  others.
- **Cross-period** — the baseline window vs a recent one. Shows change over
  time, but attributes *every* difference to adoption. It's a correlation,
  never a cause, and the output says so and lists what else moves the same
  numbers.

Revert rate is always printed next to throughput. A velocity gain that
arrives with more reverts is deferred rework, not speed, and the two
numbers only mean something together.

Thin data is flagged rather than quietly reported: under 20 commits in
either group, a rate moves several points on one or two commits.

### CI

```yaml
- run: dun check --base "origin/${{ github.base_ref }}"
```

Local hooks are advisory — `--no-verify` bypasses them. The CI check is
what makes coverage real; see [`.github/workflows/trailer-check.yml`](.github/workflows/trailer-check.yml)
for the full example.

## How attribution is determined

Right now the only adapter is **Claude Code**, read from its own local
session transcripts (`~/.claude/projects/**/*.jsonl` — a file Claude Code
already writes for its own session-resume purposes; whodunit only reads
it). At commit time, `dun` checks which staged files were touched by a
recent session and, if the exact text matches, upgrades confidence to
`intersected`.

Attribution is matched by content hash, never by commit sha — a commit
doesn't exist yet when the observation is recorded, and may later be
amended, rebased, or squashed. Hashing what changed, not where it landed,
keeps attribution correct across all three.

Cursor and Windsurf aren't supported yet — their session history lives in
an undocumented SQLite blob with no compatibility contract, and reverse-
engineering it isn't a maintenance burden this project is taking on. The
adapter interface is open for a community contribution.

## Privacy

- No network calls. Ever. The whole tool is a handful of files reading
  git, reading local transcripts, and writing a local SQLite journal.
- No prompt text, file contents, names, emails, hostnames, or remote URLs
  are ever recorded — the journal schema has no field that could hold
  them.
- The journal lives outside your repo (`.git/dun/journal/journal.db`),
  never committed, never pushed.
- `dun journal show` prints exactly what's been recorded, in full.
  `dun journal purge` deletes it.

## Status

Early. The trailer spec and local loop (hooks, journal, CI check, report)
work end to end and are dogfooded on this repo's own history. Not built
yet: an OS-level background service (today's `dun daemon run` is
foreground-only), additional agent adapters, and any hosted/aggregated
reporting.
