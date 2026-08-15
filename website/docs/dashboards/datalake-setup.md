---
id: datalake-setup
title: Setting up the datalake
sidebar_label: Setting up the datalake
---

# Setting up the datalake

**Entirely optional.** `dun` works with no database at all — the hooks, the
journal, `dun status` and `dun report` are local and need nothing. This is
for the shared view: several repositories and several people looked at
together, and AI attribution correlated with delivery data from GitHub and
your issue tracker.

The stack is [Apache DevLake](https://devlake.apache.org). whodunit
publishes into six `whodunit_`-prefixed tables and **never writes to
DevLake's own domain tables**, so its plugins and this tool coexist without
either corrupting the other.

## Two steps, deliberately separate

Step 1 builds the stack and is run once. Step 2 puts the dashboards on it
and is re-run whenever whodunit ships new ones — which is why it is not
folded into step 1.

Neither needs `dun` or a clone of the repository. This is infrastructure;
the developers who publish into it install the CLI separately.

### Step 1 — the stack

Once, on whichever machine holds the database:

```sh
curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/setup-datalake.sh | sh
```

Fetches DevLake's own compose file from upstream, generates an encryption
secret, starts four containers, and creates the Grafana datasource. From a
checkout, `./up.sh` does the same.

### Step 2 — the dashboards

Again after each whodunit release:

```sh
curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/import-dashboards.sh | sh
```

Step 1 offers to run this at the end, so a first install is one sitting.
Against a Grafana you already run, start here and pass `--grafana URL`.

Re-running replaces the dashboards in place — same uids, same URLs, no
duplicates. They land in a **Whodunit** folder rather than General, so they
do not mix with DevLake's own; `--folder NAME` puts them elsewhere.

## Point `dun` at it

On each developer machine:

```sh
dun config datalake
```

This asks for the database URL and stores the password **encrypted at
rest**, bound to that machine — not in a shell profile, and never in
plaintext in `config.json`.

Then publish:

```sh
dun sync --dry-run   # see exactly what would be sent
dun sync             # send it
```

Once configured, `git push` syncs automatically through the `pre-push`
hook. There is nothing to schedule.

## What gets published

Six tables, listed in full on the [Privacy](../reference/privacy#what-dun-sync-sends)
page. The two identifying fields are the contributor email and the file
paths, and both are named there rather than buried.

## Ports

| | |
|---|---|
| DevLake config UI | `http://localhost:4000` |
| Grafana | `http://localhost:3002` |

The DevLake stack ships with published default credentials for its local
database (`merico`/`merico`). They are upstream defaults for a local
compose stack, not secrets — but a deployment that leaves them reachable
from anywhere but localhost has a problem. Change them before exposing the
stack.

## Keeping it running

```sh
deploy/devlake/verify.sh
```

Checks the containers, the schema and the datasource, and says which is
wrong rather than that something is.
