# Local DevLake

A local [Apache DevLake](https://devlake.apache.org) stack, so whodunit has a
real database and Grafana to write into during development.

```sh
./up.sh
```

| | |
|---|---|
| Config UI | http://localhost:4000 |
| Grafana | http://localhost:3002 — `admin` / `admin` |
| MySQL | `127.0.0.1:3306` — `merico` / `merico`, database `lake` |

## What this is not

**Not a supported deployment.** It is upstream's compose file, vendored
unmodified so it stays diffable against theirs, plus a script that generates
the encryption secret DevLake refuses to start without.

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
