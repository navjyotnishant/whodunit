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
| `dun init [--repo <path>]` | Install the git hooks into a repository |
| `dun repos list` / `candidates` / `remove` | See what's instrumented, and what isn't |
| `dun status` | Trailer coverage and method mix for recent commits |
| `dun report` | Self-contained local HTML report — coverage, penetration, method mix, purpose distribution, per-commit detail |
| `dun delta` | Velocity and revert-rate comparison, before/after adoption — see below |
| `dun check --base <ref>` | Fail if any commit since `--base` lacks a valid trailer — the CI gate |
| `dun ingest [--since <time>]` | Read local agent session transcripts into the journal |
| `dun daemon run` | Foreground watcher: re-ingests continuously as sessions change |
| `dun journal show` / `dun journal purge` | Inspect or wipe the local journal |

### Which repositories are instrumented

Instrumentation is per repository and always explicit. `dun init` installs
hooks into one repo and records it; there is no flag to enrol every
repository you have ever used an agent in.

That is deliberate. The set of repos with agent transcripts includes client
work, throwaway experiments, and clones of other people's projects.
Instrumenting a repo means its commits start carrying an AI-attribution
trailer, which is a disclosure decision — it belongs to you, one repo at a
time.

```sh
dun init                      # instrument the current repository
dun init --repo ~/code/other  # instrument another one without cd'ing
dun repos list                # what is instrumented
dun repos candidates          # repos with agent activity and no hooks
dun repos remove              # stop tracking this repo for cross-repo tooling
```

`dun repos candidates` only reports. Nothing enrols a repository except
you running `dun init` in it — which also makes the registry a usable
opt-in list for anything that later works across repositories.

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
- Everything is stored outside your repositories, under `~/.whodunit`:
  the journal (`journal.db`), baseline snapshots (`baselines/`), and
  config. Nothing is ever committed or pushed.
- `dun journal show` prints exactly what's been recorded, in full.
  `dun journal purge` deletes it — only for the current repository, even
  though the store is shared.

### Where data lives

| Path | What |
|---|---|
| `~/.whodunit/config.json` | Global settings (subscription spend, retention) |
| `~/.whodunit/repos.json` | Which repositories you instrumented with `dun init` |
| `~/.whodunit/data/journal.db` | Observations, one row per agent edit, scoped by repository |
| `~/.whodunit/baselines/<repo>.json` | Pre-adoption snapshots (`dun baseline capture`) |

Everything is owner-only (`0700` directories, `0600` files) and repaired on
open if it was ever created more permissively — the journal records which
files you edited and when, which is nobody else's business on a shared
machine.

Baselines sit apart from `data/` because they are the one thing here that
is *not* regenerable: `dun ingest` can rebuild the journal at any time, but
a pre-adoption window closes permanently.

The journal is one shared database rather than a file per repository, and
rows carry a `repo_id` column. That identifier is the repository's **root
commit SHA** — stable across clones, machines, and directory moves, and
revealing nothing on its own. A filesystem path would break as soon as the
repo moved; a remote URL would record the org and repo name, which this
tool never stores.

Scoping by column rather than by file also means the eventual move to a
shared Postgres or Mongo backend is a driver change, not a redesign: a
server has one table for everything either way.

## Status

Early. The trailer spec and local loop (hooks, journal, CI check, report)
work end to end and are dogfooded on this repo's own history. Not built
yet: an OS-level background service (today's `dun daemon run` is
foreground-only), additional agent adapters, and any hosted/aggregated
reporting.
