# The datalake

An [Apache DevLake](https://devlake.apache.org) stack for whodunit to publish
into, so several repositories and several people can be looked at together —
and so AI attribution can be correlated with delivery data from GitHub and
your issue tracker.

**Entirely optional.** `dun` works with no database at all; everything here is
for the shared view.

## Setting it up

Two steps, deliberately separate. Step 1 builds the stack and is run once.
Step 2 puts the dashboards on it and is re-run whenever whodunit ships new
ones — which is why it is not folded into step 1.

Neither needs `dun`, or a clone of this repository. This is infrastructure;
the developers who publish into it install the CLI separately.

**Step 1 — the stack** (once, on whichever machine holds the database):

```sh
curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/setup-datalake.sh | sh
```

Fetches DevLake's own compose file from upstream, generates an encryption
secret, starts four containers, and creates the Grafana datasource. From a
checkout, `./up.sh` does the same thing.

**Step 2 — the dashboards** (again after each whodunit release):

```sh
curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/import-dashboards.sh | sh
```

Step 1 offers to run this at the end, so a first install is one sitting.
Against a Grafana you already run, start here and pass `--grafana URL`.
Re-running replaces the dashboards in place — same uids, same URLs, no
duplicates.

| | |
|---|---|
| Config UI | http://localhost:4000 |
| Grafana | http://localhost:3002 — `admin` / `admin` on first start |
| MySQL | `127.0.0.1:3306` — `merico` / `merico`, database `lake` |

## Publishing into it

On a developer machine, pointing at wherever step 1 ran:

```sh
dun config datalake     # host:3306, database lake, user merico
dun sync                # or just git push
```

The `whodunit_*` tables are created by that first sync — there is no
migration step. They live in the same `lake` database and never touch
DevLake's own tables.

Dashboards imported before any data exists render empty rather than broken.
Sync first if you want to see something.

## The dashboards

| File | Answers |
|---|---|
| `whodunit.json` | coverage, penetration, method mix |
| `whodunit-adoption.json` | sessions, agents, tools, acceptance |
| `whodunit-exec.json` | cycle time for AI-assisted work vs the rest |
| `whodunit-dora.json` | does adoption move delivery — DORA against attribution |
| `whodunit-hours.json` | when and how the agent is used — rhythm, tools, session shape |
| `whodunit-funnel.json` | adoption vs value, in six independently measured stages |
| `whodunit-cost.json` | what it cost in tokens, per model — with cache efficiency and the break-even marked |

They are also attached to every GitHub release, so a team can pin a version
rather than tracking `main`:

```sh
curl -fsSL …/import-dashboards.sh | sh -s -- --version v0.2.0
```

### Cost is reported in tokens, never in currency

`whodunit-cost.json` shows measured token counts and deliberately stops
short of a price. Under a subscription the marginal cost of a token is
zero — a user on a fixed monthly plan spends the same whether a session
burns 10k tokens or 10M — so multiplying by an API rate would report money
nobody spent. Nothing in a transcript says which billing model a user is
on, so the tool would be guessing at the pricing model before reaching the
price. Anyone who needs a figure has their own contract and can multiply.

Two numbers on that dashboard are easy to get wrong in a flattering
direction, and both are worth knowing before reading it:

**Cache writes count as uncached** in the read-ratio panel. A write
arrives uncached and is billed above base rate, so leaving it out of the
denominator turns a real 48% into 99% — measured, on this project's own
data. A panel showing 99% recommends nothing.

**Break-even for write payback is 1.25x, not 1.0x.** A write costs about
1.25x base and a read 0.1x, so a write needs roughly 1.25 reads to pay for
itself. A model sitting at 1.10x lost money while looking healthy against
a 1.0 line, which is why the threshold is drawn where it is.

Panels are empty rather than zero where an agent cannot report: Antigravity
records no tokens or timing at all, and Codex reports cache reads but never
writes. A zero on a cost panel reads as "this agent is free".

### The DORA dashboard needs DevLake configured

The other five read only the `whodunit_*` tables the CLI syncs, so they work
as soon as `dun sync` has run. `whodunit-dora.json` joins that data to
DevLake's own delivery metrics, and those come from GitHub and your issue
tracker — which means three settings have to be right first.

None of them announces itself when wrong: DevLake collects, reports success,
and produces an empty dashboard. All three were found the slow way.

**1. The GitHub token needs `read:user`.** DevLake's GraphQL issue collector
requests each author's `email` field. Without the scope, GitHub rejects the
whole query, `Collect Issues` fails, and the pipeline dies *before* reaching
pull requests or commits — so a repository can be configured, collected
nightly, and still have no row in `repos` at all. Classic PATs need the scope
added explicitly; check with:

```bash
curl -sI -H "Authorization: token <token>" https://api.github.com/user | grep -i x-oauth-scopes
```

**2. Deployments need a `deploymentPattern`.** DevLake does not guess which CI
runs are deployments. Until a scope config sets the pattern, every row in
`cicd_tasks` has an empty `type`, `cicd_deployment_commits` stays at zero, and
**all four DORA metrics read empty** — they all route through that table.

In the UI: **Connections → GitHub → your connection → the repository's Scope
Config → CI/CD**. Set **Deployment** (not "Environment name" — a value in the
wrong field silently classifies nothing) to something like `(?i)deploy`, which
matches a job named "Deploy to OCI Server" while excluding test, lint and
check runs. Verify:

```bash
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT type, COUNT(*) FROM cicd_tasks GROUP BY type;"
```

**3. A project should be one deliverable.** Every DORA panel filters by
project, so the mapping decides what the numbers mean. Map the repositories
that ship together as one project — three repos deployed as one service is one
project, not three, or a single release counts three times.

The failure mode in the other direction is worse and quieter: a project
holding every repository in the account produces plausible-looking numbers
that describe nothing. A project needs its **repos** scope for pull requests
and its **cicd_scopes** scope for deployments; with only the latter,
deployments appear and `project_pr_metrics` stays empty.

**MTTR needs incidents**, which most trackers do not provide for free: the
`issueTypeIncident` regex matches against `issues.original_type`, and if the
tracker sends no type at all, no pattern will match it. The dashboard says so
in the panel rather than showing a blank.

### Why import rather than provision

An earlier version mounted the dashboards into Grafana so they appeared
automatically, which was convenient and cost more than it was worth: it kept
the compose file diverged from upstream so nobody with their own DevLake could
use it, and it made every dashboard read-only in the UI — awkward if you want
to adjust a panel and keep it.

The import path works the same way on the bundled stack and on a Grafana that
already exists, which is the point.

### The datasource, and why step 1 creates it

DevLake's Grafana is supposed to create its own `mysql` datasource at startup.
It does not: the entrypoint authenticates as
`admin:$GF_SECURITY_ADMIN_PASSWORD`, which upstream's compose file never sets,
so every call fails and no datasource appears —

```
Deleting old MySQL datasources...
... POST /api/datasources status=401 error="no password provided"
```

Step 1 creates it, named `mysql` because DevLake's own dashboards bind to that
name, so one datasource serves both theirs and ours. Step 2 finds whatever
mysql datasource exists rather than assuming a uid, and `--datasource <uid>`
pins a specific one.

### Where DevLake itself comes from

Not from this repository. `setup-datalake.sh` fetches `docker-compose.yml` and
`env.example` from the pinned upstream release, and both are gitignored here.

We vendored them once. The copy differed from Apache's by nine lines of
comment, which is not a fork worth maintaining — it just went stale every time
DevLake released. Note the compose file is published **only as a release
asset**, not in upstream's git tree, so there is no raw URL for it.

### Editing them

`dashboards/` holds the canonical files; `dashboards-import/` is generated
from them, with the datasource replaced by a placeholder the import dialog
fills in. Never hand-edit the generated ones.

```sh
./build-cost-dashboard.py        # regenerate the cost dashboard
./build-funnel-dashboard.py      # regenerate the funnel
./export-dashboards.py           # regenerate the importable copies
./export-dashboards.py --check   # what CI runs
```

Adjusted a panel in Grafana and want to keep it? **Share → Export → Save to
file**, drop it into `dashboards/`, and regenerate.

CI fails if the two are out of step. Two hand-maintained copies of a
22-panel dashboard drift within a month, and the drift is invisible until
someone imports the stale one.

The generated copies keep their uids (`whodunit-funnel` and friends), which is
what makes re-importing replace a dashboard rather than adding a second copy
beside it. Grafana's own "export for sharing" blanks the uid; doing that here
turned six dashboards into twelve on the first re-run.

If you need to create the datasource by hand — a Grafana that DevLake did not
set up, or step 1's attempt failed — it must be a MySQL datasource pointing at
the `lake` database:

```sh
curl -u admin:YOUR_PASSWORD -X POST http://localhost:3002/api/datasources \
  -H 'Content-Type: application/json' \
  -d '{"name":"mysql","type":"mysql","access":"proxy","url":"mysql:3306",
       "database":"lake","user":"merico",
       "secureJsonData":{"password":"merico"},"isDefault":true}'
```

Name it `mysql` if you can. Every stock DevLake dashboard references its
datasource by that literal name, so anything else leaves *their* panels
reporting *"datasource mysql wasn't found"* — ours bind by uid at import and
do not care.


### If the devlake container restarts in a loop

Symptom: `docker compose ps` shows `devlake: Restarting`, while mysql,
grafana and config-ui stay up — so the dashboards still render and only
collection is dead. The logs repeat:

```
Scan error on column index 0, name "created_at": unsupported Scan,
storing driver.Value type []uint8 into type *time.Time
```

That is the Go MySQL driver refusing to read a `datetime` into a
`time.Time`, which it can only do when the DSN carries `parseTime=True`.
`.env` ships with it, so the usual cause is a container created before the
current `.env` and never recreated — `docker compose restart` reuses the
old environment, so it restarts forever with the same stale value.

Check what the container actually has, rather than what `.env` says:

```sh
docker inspect devlake-devlake-1 \
  --format '{{range .Config.Env}}{{println .}}{{end}}' | grep DB_URL
```

If the query string is missing, recreate it:

```sh
docker compose up -d --force-recreate devlake
```

`--force-recreate` is the point: without it Compose sees a container that
already exists and leaves its environment alone.

## What this is not

**Not a supported deployment.** It is upstream's compose file, fetched
unmodified, plus a script that generates the encryption secret DevLake refuses
to start without and creates the datasource its own entrypoint fails to.

**Not secure.** The credentials above are DevLake's published defaults and are
in this repository. MySQL is bound to `3306` on your machine. Fine for
evaluating on a laptop; do not put this on a network anyone else can reach.

**Not required.** whodunit works without any of this — `dun report` renders a
self-contained HTML file with no server at all. DevLake matters when several
repositories or several people's data need to be looked at together.

## Why DevLake rather than just a database and Grafana

Nothing here uses DevLake's own collectors: whodunit's data comes from git and
the local journal, not from GitHub or Jira. A bare Postgres and Grafana would
serve the same panels.

DevLake is the target because a team that already runs it should be able to add
whodunit without standing up a second stack. That only works if our tables
coexist with theirs, which is why every table is prefixed `whodunit_` and
nothing writes to DevLake's domain tables — their schema shifts between minor
versions, and writing into it would make every DevLake upgrade a potential
data-loss event for us.

Testing against a real DevLake database is the only way to know that
coexistence actually holds.

## Upgrading

**DevLake** is pinned in `setup-datalake.sh` (`DEVLAKE_VERSION`). To move,
delete the fetched files and re-run with the tag you want:

```sh
rm docker-compose.yml env.example
sh setup-datalake.sh --version v1.0.3-beta16
```

Your `.env` is not touched — delete it if you want the new `env.example`
defaults, but keep your `ENCRYPTION_SECRET` or DevLake will not read back
anything it previously encrypted.

Releases are at <https://github.com/apache/devlake/releases>. (The repository
moved from `apache/incubator-devlake`; the old name still redirects.)

**The dashboards** upgrade on their own schedule — re-run step 2. Nothing else
is touched, and the existing dashboards are replaced rather than duplicated.
