# Local DevLake

A local [Apache DevLake](https://devlake.apache.org) stack, so whodunit has a
real database and Grafana to write into during development.

```sh
./up.sh
```

| | |
|---|---|
| Config UI | http://localhost:4000 |
| Grafana | http://localhost:3002 — `admin` / `admin` on first start |
| MySQL | `127.0.0.1:3306` — `merico` / `merico`, database `lake` |

## The dashboards

Import them once through the Grafana UI: **Dashboards → New → Import**,
upload a file from `dashboards-import/`, and pick your MySQL datasource when
it asks.

| File | Answers |
|---|---|
| `whodunit.json` | coverage, penetration, method mix |
| `whodunit-adoption.json` | sessions, agents, tools, acceptance |
| `whodunit-exec.json` | cycle time for AI-assisted work vs the rest |
| `whodunit-dora.json` | does adoption move delivery — DORA against attribution |
| `whodunit-hours.json` | when and how the agent is used — rhythm, tools, session shape |
| `whodunit-funnel.json` | adoption vs value, in six independently measured stages |

They are also attached to every GitHub release, so a team can import them
without cloning this repository.

### The DORA dashboard needs DevLake configured

The other three read only the `whodunit_*` tables the CLI syncs, so they work
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

`docker-compose.yml` here is stock DevLake. An earlier version mounted the
dashboards into Grafana so they appeared automatically, which was convenient
and cost more than it was worth: it kept this file diverged from upstream so
nobody with their own DevLake could use it, and it made every dashboard
read-only in the UI — awkward if you want to adjust a panel and keep it.

The import path works the same way on the bundled stack and on a Grafana
that already exists, which is the point.

### Editing them

`dashboards/` holds the canonical files; `dashboards-import/` is generated
from them, with the datasource replaced by a placeholder the import dialog
fills in. Never hand-edit the generated ones.

```sh
./export-dashboards.py           # regenerate after changing a dashboard
./export-dashboards.py --check   # what CI runs
```

Adjusted a panel in Grafana and want to keep it? **Share → Export → Save to
file**, drop it into `dashboards/`, and regenerate.

CI fails if the two are out of step. Two hand-maintained copies of a
22-panel dashboard drift within a month, and the drift is invisible until
someone imports the stale one.

They need a MySQL datasource named **`mysql`** pointing at the `lake`
database. That one step is still manual: the image's entrypoint rewrites
`/etc/grafana/provisioning/datasources/datasource.yml` on every start, so
mounting our own file there read-only makes Grafana crash-loop. Create it
once:

```sh
curl -u admin:YOUR_PASSWORD -X POST http://localhost:3002/api/datasources \
  -H 'Content-Type: application/json' \
  -d '{"name":"mysql","type":"mysql","access":"proxy","url":"mysql:3306",
       "database":"lake","user":"merico",
       "secureJsonData":{"password":"merico"},"isDefault":true}'
```

The name matters. Every stock DevLake dashboard references its datasource by
the literal name `mysql`, so a datasource called anything else leaves them
reporting *"datasource mysql wasn't found"* on every panel.

## What this is not

**Not a supported deployment.** It is upstream's compose file plus a script
that generates the encryption secret DevLake refuses to start without. The
only change to their file is the Grafana volume mounts that provision the two
dashboards, kept to one block so it stays diffable against theirs.

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

The compose file is pinned to a DevLake release. To move:

```sh
gh release download <tag> --repo apache/incubator-devlake \
  --pattern docker-compose.yml --pattern env.example --clobber
```

Then re-run `./up.sh`. Your `.env` is not overwritten — delete it if you want
the new `env.example` defaults, but keep your `ENCRYPTION_SECRET` or DevLake
will not read back anything it previously encrypted.
