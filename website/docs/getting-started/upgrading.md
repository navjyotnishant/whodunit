---
id: upgrading
title: Upgrading
sidebar_label: Upgrading
description: Upgrading the dun binary, what happens to your hooks, and re-importing the dashboards.
---

# Upgrading

There are three things a whodunit install consists of, and they upgrade
independently: the `dun` binary, the git hooks inside each instrumented
repository, and the Grafana dashboards.

**Only the first one needs you to do anything.** The hooks repair themselves
and the dashboards are a single re-import.

## Knowing an upgrade exists

`dun` tells you once a day when a newer release exists, on a bare `dun` only
— never from a git hook, because a version check that can hang a commit is
worse than an out-of-date binary. Turn it off with
`dun config set version_check off`, or `DUN_NO_VERSION_CHECK` /
`DO_NOT_TRACK` in the environment.

## The binary

Whichever way you installed it:

```bash
brew upgrade dun                    # Homebrew (macOS, Linux)
scoop update dun                    # Scoop (Windows)
go install github.com/navjyotnishant/whodunit/cmd/dun@latest
```

Or download the archive for your platform from the
[releases page](https://github.com/navjyotnishant/whodunit/releases) and
replace the binary on your `PATH`.

Check what you have:

```bash
dun version
```

### If `brew upgrade` says a version is already installed

```text
Warning: navjyotnishant/tap/dun 0.3.0 already installed
```

That message means Homebrew has no newer version *in its tap*, which is not
the same as no newer version existing. Refresh the tap first:

```bash
brew update && brew upgrade dun
```

If it still reports the old version while the releases page shows a newer
one, the tap formula has not been published for that release yet. Install
the release directly in the meantime:

```bash
go install github.com/navjyotnishant/whodunit/cmd/dun@v0.3.1
```

## The hooks

`dun init` writes hooks into a repository's `.git/hooks`. Upgrading the
binary reaches all of them for free, because the hooks resolve `dun` from
`PATH` at run time rather than hardcoding a path.

Two kinds of change do not propagate that way: a hook that did not exist
when the repository was instrumented, and a change to the hook script's own
shape. Both have happened — `pre-push` was added after some repositories
were already instrumented, and those simply never synced, with nothing to
indicate anything was missing.

**So the repair is automatic.** The next time you run a `dun` command in a
repository, stale or missing hooks are rewritten and a single line says so.
Nothing is printed when there is nothing to do.

The repair deliberately does not run from inside the hooks themselves. A
commit hook sits on the critical path of every commit, and a hook that
rewrites hooks mid-commit is both slow and surprising — it would be
rewriting the script currently executing. Detection belongs where a person
is reading output.

An existing non-whodunit hook is preserved by chaining to it rather than
replaced, which is what makes an unattended repair safe.

### Repairing every repository at once

If you would rather not wait to visit each repository:

```bash
dun repos update
```

This is the bulk path, not a required step. It reinstalls hooks in every
repository that has had `dun init` run in it.

## The dashboards

Dashboards change when whodunit ships a release, not when the stack is
built. Re-run step 2 of the datalake setup:

```bash
./deploy/devlake/import-dashboards.sh
```

Or, against a Grafana someone else runs, with no clone and no Docker:

```bash
curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/import-dashboards.sh | sh
```

The dashboards keep the same uids, so a re-import replaces them rather than
creating duplicates. **Any panel edits you made in the Grafana UI are
overwritten**, which is the trade for being able to ship fixes.

By default the script fetches the dashboards from `main`. Pin them to a
release instead:

```bash
WHODUNIT_VERSION=v0.3.1 ./deploy/devlake/import-dashboards.sh
```

Nothing else in the stack is touched — not the datasource, not the folder
permissions, not DevLake's own dashboards.

## What upgrading does not touch

Your journal at `~/.whodunit/data/journal.db`, your configuration, and the
trailers already written into your commit history are all left alone. An
upgrade adds and repairs; it does not migrate away data you have already
collected.

## Verifying

```bash
dun version    # the binary you just installed
dun status       # repairs hooks if needed, and reports the sync backlog
```

If `dun status` reports a backlog that does not shrink after a push, that is
a sync configuration question rather than an upgrade one — see
[Connecting delivery data](../dashboards/connecting-your-data.md).
