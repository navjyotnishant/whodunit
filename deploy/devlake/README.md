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

Three dashboards live in `dashboards/`, mounted into Grafana's dashboard
directory, which it rescans every few seconds. Edit one and the change
appears without an import, which is what keeps the files in this repository
the source of truth rather than a copy of whatever was last pasted into
Grafana. Adding a dashboard is dropping a file in — no compose change.

| | Answers |
|---|---|
| `whodunit.json` | coverage, penetration, method mix |
| `whodunit-adoption.json` | sessions, agents, tools, acceptance |
| `whodunit-exec.json` | cycle time for AI-assisted work vs the rest |

### Using them in your own Grafana

The mounted copies pin a datasource named literally `mysql`, because every
stock DevLake dashboard does. That works here and nowhere else.

`dashboards-import/` holds the same dashboards exported for external use:
the datasource is a placeholder Grafana asks you to fill in at import time.
Take a file from there, **Dashboards → New → Import**, pick your MySQL
datasource, done. They are also attached to every GitHub release, so a team
can use them without cloning this repository.

Those files are generated, never hand-edited:

```sh
./export-dashboards.py           # regenerate after changing a dashboard
./export-dashboards.py --check   # what CI runs
```

CI fails if they are out of date. Two hand-maintained copies of a 22-panel
dashboard drift within a month, and the drift is invisible until someone
imports the stale one.

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
