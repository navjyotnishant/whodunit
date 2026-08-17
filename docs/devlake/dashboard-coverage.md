# DevLake dashboard coverage

Which of DevLake's dashboards work against this deployment's data, which are
one setting away, and which cannot work until something upstream changes.

Every verdict below was produced by **running each panel's SQL against MySQL**,
not by reading the schema. That distinction matters: `cicd_deployment_commits`
held 211 rows while every DORA panel rendered empty, because all 211 carried
`environment='github-pages'` and the panels filter `PRODUCTION`. Only executing
the query shows that.

**Last audited: 2026-08-13** — 125 panels across 11 dashboards.

## Why this document exists

DevLake fails quietly. A misconfigured connection collects successfully,
reports success, and renders an empty panel. Three such faults were found in a
single afternoon — a GitHub token missing the `read:user` scope, an unset
`deploymentPattern`, and a project holding 22 unrelated repositories — and each
had been silently wrong for weeks.

Without a map, an empty panel is indistinguishable from a broken one, and the
diagnosis gets repeated from scratch every time. This is the map.

## Scope

Eleven dashboards: the six DORA ones and five Engineering ones. Excluded, and
why:

- **31 connector dashboards** (Jenkins, Bitbucket, TAPD, Zentao, Jira…) — no
  connection configured, and none planned.
- **7 `Demo-*` dashboards** — DevLake's stock samples, carrying fabricated
  data.
- **6 whodunit dashboards** — they read the `whodunit_*` tables the CLI syncs
  directly, are unaffected by every gap here, and are known-good.

## Two traps to know before reading any panel

**"Returns rows" is not "shows data."** Several DORA panels return exactly one
row whose *content* is the string `N/A. Please check if you have collected
incidents.` It renders as text in the panel. That is a placeholder, not a
metric.

**The time window changes the DORA verdict.** Over a fixed January–December
window, Engagehub's deployment frequency reads *"Fewer than once per month
(low)"*. Over the last 90 days, the same underlying data reads *"Between once
per day and once per week (high)"*.

Neither is a bug. The rating is a median over buckets, and the wide window
includes eight months of zeros because collection only spans May–August. **Read
the DORA dashboards on a 90-day window**, and treat any rating quoted from a
wider one as an artifact.

## Per-dashboard verdicts

| Dashboard | Panels | Verdict |
|---|---|---|
| DORA Details — Deployment Frequency | 8 SQL | **WORKS** — Engagehub, 154 deployments |
| Engineering Overview | 18 SQL | PARTIAL — 11 work |
| Engineering Throughput and Cycle Time | 12 SQL | PARTIAL — 11 for whodunit, 2 for Engagehub |
| Contributor Experience | 8 SQL | PARTIAL — 7 work, never simultaneously |
| Component and File-level Metrics | 10 SQL | PARTIAL — 4 work |
| GitHub Release Quality | 24 SQL | PARTIAL — 5 work |
| DORA | 9 SQL | PARTIAL — deployment frequency only |
| DORA Details — Change Failure Rate | 4 SQL | PARTIAL — denominator only |
| DORA Details — Lead Time for Changes | 6 SQL | EMPTY |
| DORA Details — Time to Restore Service | 3 SQL | EMPTY |
| DORA (by Team) | 9 SQL | EMPTY |

More works than expected. **Engineering Overview** and **Throughput and Cycle
Time** are substantially live — mean issue lead time, developer counts, coding
days, cycle-time series — and nobody had looked.

### Evidence samples

Deployment Frequency, panel 1 (154 rows):

```
Engagehub  github:GithubRun:2:1141661297:31670159242  docs(changelog): org chart seniority level filter (ENG-249)
```

Distinct deployment days by month: `May=3 · Jun=8 · Jul=6 · Aug=10`.

Contributor Experience, the same dashboard under two different `$repo_id`
selections:

```
EngageHub Linear board:  Time To Initial Issue Response  0.312 d
                         Issue Resolution Time           0.932 d
                         Issue Response Rate within SLA  88.89 %
nj-agents GitHub repo:   PR Resolution Time              0.0104 d
                         PR Resolution Rate within SLA   100.0 %
```

Its `$repo_id` variable is populated from `repos`, but the four issue panels
join `boards`. Selecting a Linear board lights up the issue half; selecting a
GitHub repo lights up the PR half. **Neither selection lights up both.**

## The gaps

Each is **config** (the data is collected; a setting is wrong — fixable in
minutes) or **upstream** (the source system does not carry the field, so no
setting can conjure it). Conflating the two files a settings ticket for
something that needs a change in how the team records work.

| # | Root cause | Blocks | Kind |
|---|---|---|---|
| 1 | Engagehub project has no `repos` mapping — only `boards` + `cicd_scopes`, orphaning its 4 PRs | 19 panels, 3 dashboards | config |
| 2 | The scope config carrying `issue_type_bug` is attached to 1 of 47 repos; 23 bug-labelled issues sit outside it | 14 panels | config |
| 3 | `refs_issues_diffs` = 0 — the refdiff plugin is in no blueprint | 14 panels | config |
| 4 | `commit_files` = 0 — gitextractor runs without file granularity | 6 panels | config |
| 5 | `teams` = 0 — no roster uploaded | 9 panels (all of DORA-by-Team) | config¹ |
| 6 | `incidents` = 0 — nothing incident-shaped exists in GitHub or Linear | all CFR + Time-to-Restore | **upstream** |
| 7 | `issues.due_date` NULL on all 416 issues | 2 panels | **upstream** |
| 8 | No `severity:*` or `component:*` labels (0 in `issue_labels`) | 5 panels | **upstream** |
| 9 | `story_point` set on 8 of 416 issues | 1 panel (renders NULL) | config + upstream |
| 10 | No `good first issue` label | 1 panel (reads 0) | **upstream** |
| 11 | Contributor Experience hardcodes *last calendar month*, ignoring the time picker | 8 panels | neither² |

¹ Technically a setting, but the roster does not exist anywhere yet — it is
data entry, not a toggle.

² Not a fault. Every EngageHub and whodunit PR was created in August 2026, and
the panels query July. It resolves itself in September; panels demonstrably
work on repos that have July data.

**Roughly two-thirds config, one-third genuinely missing.** Gaps 1–5 cover 53
panels needing no new data — it is collected and unqueried. Gaps 6–8 and 10
need the team to start recording something it currently does not.

**Highest leverage:** #1 is one mapping row and unblocks 19 panels. #2 and #3
unblock 14 each.

## The number that is wrong rather than absent

**Change Failure Rate currently renders `0.0000%`.**

That reads as a flawless deployment record. It means *no incident data*: the
numerator is `sum(has_incident)` over an `incidents` table with zero rows, so
it is structurally zero regardless of what happened.

It also does **not** count the 25 failed deployments. That panel measures
incidents caused by *successful* deployments — a different question from
"deployments that failed".

A plausible, wrong number is worse than an empty panel, and it is exactly the
failure whodunit exists to prevent. Do not quote this figure until gap #6 is
resolved.

## Verifying a gap yourself

```bash
# What is classified, and what is not
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT type, COUNT(*) FROM cicd_tasks GROUP BY type;"

# Does a project have all three scope kinds?
docker exec devlake-mysql-1 mysql -umerico -pmerico lake \
  -e "SELECT project_name, \`table\`, COUNT(*) FROM project_mapping GROUP BY 1,2;"

# The tables behind the dark panels
docker exec devlake-mysql-1 mysql -umerico -pmerico lake -e \
  "SELECT (SELECT COUNT(*) FROM incidents) incidents,
          (SELECT COUNT(*) FROM refs_issues_diffs) refs_issues_diffs,
          (SELECT COUNT(*) FROM commit_files) commit_files,
          (SELECT COUNT(*) FROM teams) teams;"
```

A panel that renders empty should match a zero here. If it does not, the gap is
somewhere this document has not looked — worth adding.

## Related

- `deploy/devlake/README.md` — the three settings the whodunit DORA dashboard
  depends on, and how each fails silently.
- `deploy/devlake/dashboards/whodunit-dora.json` — reads `whodunit_*` plus
  DevLake's deployment tables; unaffected by gaps 2–11.

## Why the funnel's last stages are empty

`whodunit-funnel.json` lays out six stages — adoption, engagement, AI-assisted
work, efficiency, productivity, business value — and deliberately leaves the
last two blank.

Stages 5 and 6 both divide by a **pre-adoption baseline**. `dun baseline
capture` exists to record one, but none was captured here, and the window
before the hooks were installed cannot be recovered. `dun delta` reports the
same limit and refuses to compute a before/after without it.

A productivity percentage derived without that baseline would be arithmetic on
an assumption — and it would be the figure people quoted. The stages state
what is missing instead.

**Stage 4 is measurable but must stay board-scoped.** Pooled across boards the
comparison inverts: assisted issues read 2.5× *slower* (94.6h vs 38.3h) while
being faster within every board taken individually (98.6h vs 173.6h on one).
One board contributes 148 fast unassisted issues that dominate the pooled
average. That is Simpson's paradox, not an AI effect, and a panel showing the
pooled figure would be confidently wrong.

**The issue-to-commit link is a text match.** GitHub issue keys are bare
integers, so matching them against commit messages makes issue `1` match every
commit containing a "1" — 57 of 57 assisted commits. The guard is
`issue_key REGEXP '^[A-Z][A-Z0-9]+-[0-9]+$'`, which restricts the match to
tracker-style keys and reproduces the correct count.

**That guard was on one panel and missing from seven** until NAV-122. The
executive dashboard's issue panels (ids 8–13, 18) carried the unguarded join;
only the funnel's Stage 4 had the predicate. Measured per GitHub board,
unguarded against guarded, on panel 10 alone:

| board | unguarded | guarded |
|---|---|---|
| navjyotnishant/specter-agent | 11 | 0 |
| navjyotnishant/nj-agents | 4 | 0 |
| navjyotnishant/whodunit | 2 | 0 |
| a Linear board | 1 | 1 |

Every unguarded count above zero was a fabricated AI-assisted issue. Nothing
errored — the panels rendered plausible numbers.

The dashboards were correct in practice only because the `board` variable's
own query was scoped `LIKE 'linear:%'`, so a GitHub board was never selectable.
That is a property of a dropdown's default rather than of the query, and it is
exactly the kind of accidental safety that stops being safe when someone widens
the variable. `check-issue-key-guard.py` now fails the build if any panel
matches `issue_key` against a commit message without the predicate.

The same checker also catches a panel filtering on `$board` from a dashboard
that never declares the variable — the funnel was doing this, substituting an
undefined value and rendering empty rather than erroring.
