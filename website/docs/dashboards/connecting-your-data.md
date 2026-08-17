---
title: Connecting delivery data
sidebar_position: 2
description: Connecting DevLake to GitHub and your issue tracker, and the three settings that silently produce an empty DORA dashboard.
---

# Connecting delivery data

Six of the seven dashboards read only the `whodunit_*` tables and work the
moment `dun sync` has run. **AI Impact on Delivery is the exception.** It joins
attribution to DevLake's own delivery metrics, which means DevLake has to be
collecting from GitHub and your issue tracker first.

This page is about that half. It is the part where a new operator loses an
afternoon, because none of the three ways it fails announces itself: DevLake
collects, reports success, and renders an empty dashboard.

## Credentials, and changing them

The bundled stack starts with DevLake's published defaults:

| Service | User | Password |
|---|---|---|
| Grafana | `admin` | `admin` |
| MySQL | `merico` | `merico` |

Those are upstream's documented values, not secrets this project invented.
Grafana prompts for a new password on first login, and you should set one.

**This stack is not a secure deployment and is not meant to be.** MySQL is
bound to port 3306 on your machine, the credentials above are in a public
repository, and the compose file is upstream's, fetched unmodified. It is
appropriate for evaluating on a laptop or running behind a VPN. Do not put it
on a network other people can reach without putting real authentication in
front of it.

## Connecting a tracker

In the DevLake config UI (`http://localhost:4000`), go to **Connections** and
add the systems you use. GitHub, Jira, Linear and Bitbucket are all supported;
whodunit reads whatever DevLake collects.

Each connection needs a token, and this is where the first silent failure
lives.

### The GitHub token needs `read:user`

DevLake's GraphQL issue collector asks for each author's `email` field.
Without the `read:user` scope, GitHub rejects **the entire query**, the
`Collect Issues` task fails, and the pipeline dies *before* it reaches pull
requests or commits.

The symptom is confusing: a repository can be configured, collected nightly,
and still have no row in `repos` at all. Nothing about the failure points at a
missing scope.

Classic personal access tokens need the scope added explicitly. Check an
existing token:

```bash
curl -sI -H "Authorization: token <token>" https://api.github.com/user \
  | grep -i x-oauth-scopes
```

If `read:user` is not in that list, regenerate the token with it.

## Creating a project

A DevLake **project** is what every DORA panel filters by, so the mapping
decides what the numbers mean.

**Map the repositories that ship together as one project.** Three repositories
deployed as a single service are one project, not three. Split them and one
release counts three times.

The failure in the other direction is quieter and worse: a project holding
every repository in the account produces plausible-looking numbers that
describe nothing in particular. Nobody notices, because the dashboard looks
fine.

A project needs both scopes to be complete:

- its **repos** scope, for pull requests
- its **cicd_scopes** scope, for deployments

With only the second, deployments appear and `project_pr_metrics` stays empty.

## Deployments need a pattern

DevLake does not guess which CI runs are deployments. Until a scope config
tells it, every row in `cicd_tasks` has an empty `type`,
`cicd_deployment_commits` stays at zero, and **all four DORA metrics read
empty**, because all four route through that table.

In the UI: **Connections → GitHub → your connection → the repository's Scope
Config → CI/CD**. Set **Deployment** to a pattern like:

```text
(?i)deploy
```

That matches a job named "Deploy to Production" while excluding test, lint and
check runs.

Set it in the **Deployment** field specifically. A pattern in "Environment
name" is accepted, classifies nothing, and gives you the same empty dashboard
with no error.

Verify it took:

```bash
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT type, COUNT(*) FROM cicd_tasks GROUP BY type;"
```

Rows with a non-empty `type` mean the pattern matched.

## Mean time to recovery needs incidents

MTTR is the one metric most trackers will not give you for free. DevLake
matches `issueTypeIncident` against `issues.original_type`, so if your tracker
sends no type at all, no pattern can match it.

The dashboard says so in the panel rather than rendering a blank, which is the
intended behaviour rather than a bug to report.

## Re-importing after an upgrade

Step 2 of the install is re-run after each whodunit release:

```bash
./deploy/devlake/import-dashboards.sh
```

The dashboards keep the same uids, so a re-import replaces them rather than
creating duplicates. Any panel edits you made in the Grafana UI are
overwritten, which is the trade for being able to ship fixes.

## Troubleshooting

### The devlake container restarts in a loop

`docker compose ps` shows `devlake: Restarting` while mysql, grafana and
config-ui stay up. The dashboards still render, so only collection is dead.
The logs repeat:

```text
Scan error on column index 0, name "created_at": unsupported Scan,
storing driver.Value type []uint8 into type *time.Time
```

That is the Go MySQL driver refusing to read a `datetime` into a `time.Time`,
which it can only do when the DSN carries `parseTime=True`. The shipped `.env`
has it, so the usual cause is a container created before the current `.env`
and never recreated. `docker compose restart` reuses the old environment, so
it restarts forever with the same stale value.

Check what the container actually has, rather than what `.env` says:

```bash
docker inspect devlake-devlake-1 \
  --format '{{range .Config.Env}}{{println .}}{{end}}' | grep DB_URL
```

If the query string is missing:

```bash
docker compose up -d --force-recreate devlake
```

`--force-recreate` is the point. Without it, Compose sees an existing
container and leaves its environment alone.

### Grafana has no `mysql` datasource

DevLake's Grafana is supposed to create one at startup and does not: its
entrypoint authenticates as `admin:$GF_SECURITY_ADMIN_PASSWORD`, which
upstream's compose file never sets, so every call returns 401 and no
datasource appears.

`setup-datalake.sh` creates it, named `mysql` because DevLake's own dashboards
bind to that name. One datasource serves both theirs and ours. If you are
running against a Grafana that already exists, pass `--datasource <uid>` to the
import script to pin a specific one.

### The dashboard is empty and none of the above applies

Work outward from the data:

```bash
# did anything sync at all?
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT COUNT(*) FROM whodunit_commits;"

# did DevLake collect the repository?
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT id, name FROM repos;"
```

An empty `whodunit_commits` is a `dun sync` problem on the developer machine,
not a DevLake one. An empty `repos` with a configured connection is almost
always the `read:user` scope above.
