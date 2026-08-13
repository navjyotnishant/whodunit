# Changelog

All notable changes to whodunit are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- The sync password is encrypted at rest with AES-256-GCM under a machine-derived key, replacing environment-variable-only storage that pushed users toward a plaintext credential in `~/.zshrc`. It protects the credential if the file leaves the machine — a backup, a synced dotfiles repo, a copied home directory — but not against a process already running as your user. `dun verify` reports where the password lives and fails when either file is readable by others.

## [0.2.0] - 2026-08-13

### Added

- `dun verify` — one command that checks whether attribution is actually working: install, agents, hooks, journal, sync, and every registered repository, each finding paired with the command that fixes it. Output is grouped into sections with status icons, and the exit code is non-zero only for genuine breakage, so it can gate CI without failing a deliberate local-only setup.
- `dun sync` publishes attribution data to a shared DevLake database, with `dun config datalake` walking through host, database, user and credentials and testing the connection before saving. Publishing then happens automatically on push; the push hook never blocks a push and retries on the next one.
- `dun status` now works outside a git repository, printing a one-line summary of every instrumented repository, and takes `--repo <path|id>` to report on a specific repository from anywhere.
- `dun journal show` and `dun journal purge` take `--repo <path|id>` to inspect or clear another repository's data; purge names the repository before deleting.
- `dun delta` compares velocity and revert rate two ways — assisted vs. undetermined commits in the same window, and a baseline window vs. a recent one — flagging thin data, mismatched windows, and a missing baseline rather than printing a number that looks solid.
- `dun baseline capture` records a dated, immutable pre-adoption snapshot (commit volume, median diff size, revert rate, cadence, purpose mix) so a later before/after comparison has something to compare against.
- `dun init --repo` instruments a repository without changing directory, and a registry records what was instrumented: `dun repos list`, `dun repos candidates` (repositories with agent activity but no hooks), and `dun repos remove`.
- `dun update` upgrades the binary and rewrites hooks everywhere in one step; `dun repos update` rewrites hooks in every registered repository, preserving hooks whodunit did not write. Hooks now record the version that wrote them, so `dun verify` can report stale ones.
- Codex CLI and Antigravity CLI (`agy`) adapters, both reaching `intersected` confidence, alongside Claude Code.
- Transcript locations are configurable per agent via `dun config set agent.<name>.path` or `WHODUNIT_<AGENT>_PATH`, and `dun init` reports which agents it found while you are still watching.
- `dun report` gains inline SVG charts, journal-derived data (acceptance outcomes, per-tool and per-file activity, session engagement), and three presets — `exec`, `adoption`, `detail`. The report stays a single self-contained file with no external requests.
- Commits are classified by purpose (feature, fix, test, docs…) from Conventional Commits type, path heuristics and diff shape, and shown in the report as a distribution plus a commit-by-commit table.
- Grafana dashboards for the synced data — adoption, executive summary, and a coverage overview — including an export variant any Grafana can import, shipped as a tarball on each release.
- Bare `dun` prints a welcome screen explaining what the tool is, whether the current repository is instrumented, and the next command to run; output across commands is color-coded, honoring `NO_COLOR`, non-TTY output, `TERM=dumb`, and CI.
- Confidence levels explain themselves wherever they appear — `dun status`, the cross-repo listing, and the HTML report — with each method's position on the ladder stated (`intersected` strongest, `observed` weaker because the agent's text was changed before committing).
- Tool calls are classified as accepted, rejected, failed, or unknown, and acceptance rates always display next to the denominator they came from.

### Changed

- **Breaking:** the `session` trailer value is now a repo-scoped hash rather than the agent's raw session id, which on Claude Code doubled as the transcript filename. Existing commits keep their original value; new commits are no longer a pointer into local transcript files and cannot be correlated across repositories.
- **Breaking:** the journal moved from one SQLite file per repository under `.git/dun/` to a single database under `~/.whodunit`, keyed by repository root-commit SHA. Baseline snapshots moved to `~/.whodunit/baselines/` so they survive a fresh clone or `git clean -xfd`.
- `~/.whodunit` separates settings (`config.json`) from regenerable data (`data/journal.db`) and non-regenerable baselines.
- `ratio` is a real measurement instead of a hardcoded `1.00` — the agent's deduplicated share of a commit over total changed lines. It is omitted rather than reported as `0.00` when there is no line-level evidence, and shares below 0.005 are omitted rather than rounded to zero.
- Attribution matches line by line, file-scoped, instead of whole tool outputs against whole diff hunks, so an agent's write still matches on the lines a developer kept. Only hashes are stored, never the lines themselves.
- The commit hook writes the journal itself, so a normal install reaches `intersected` without anyone remembering to run `dun ingest`.
- Repository identity, contributor email (from `git config user.email`, captured at `dun init`) and per-repository metadata are recorded once per repository rather than on every row, and `dun journal purge` removes them too.
- `dun report` writes to the system temp directory by default and prints an absolute `file://` URL.
- Repeat `dun ingest` runs are roughly 7× cheaper on unchanged transcripts, and the commit hook stays well inside a 2-second budget.
- DevLake dashboards are imported rather than mounted, so the bundled compose file stays stock and dashboards remain editable in Grafana.

### Fixed

- Commands run outside a git repository say what is wrong and how to proceed (`use --repo <path>`) instead of surfacing `exit status 128`.
- `dun verify` no longer reports per-repository agent session counts when run outside a repository, and distinguishes an agent that is not installed from one whose configured path is wrong.
- `dun delta` counted every assisted commit as undetermined when the trailer sat at the end of a multi-line commit body.
- Release builds report their version instead of reporting none.
- Journal directories are created `0700` and the database `0600`, and existing over-permissive files are repaired on open — the journal records which files were edited and when, and was previously world-readable.
- The schema applies cleanly to MySQL as well as SQLite, fixing three portability bugs (a `TEXT` default, index creation syntax, and an over-long primary key) that made a live deployment impossible.
- `dun check` in CI no longer fails on GitHub's synthetic merge commit, which was never authored through the hooks.

## [0.1.0] - 2026-08-11

Initial release.

[Unreleased]: https://github.com/navjyotnishant/whodunit/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/navjyotnishant/whodunit/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/navjyotnishant/whodunit/releases/tag/v0.1.0
