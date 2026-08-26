# 0001 — The contributor key on `whodunit_repos`

- **Status:** accepted
- **Date:** 2026-08-25
- **Ticket:** WHO-168, under the WHO-167 epic
- **Supersedes:** the note at `internal/sidecar/schema.go:30` that contributor
  "lives on repos, not on every commit row"

## The problem

`whodunit_repos` is keyed on `repo_id` alone, and `repo_id` is the repository's
root commit SHA — identical for everyone who clones it. Two people syncing the
same repository write the same row.

The upsert makes that a silent overwrite rather than an error
(`internal/sidecar/sync.go:234`):

```sql
INSERT INTO whodunit_repos (repo_id, contributor, spec_version, synced_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(repo_id) DO UPDATE SET contributor = excluded.contributor, ...
```

Whoever pushes last owns the row. Every commit, event and session already
synced by the first person is then attributed, through the join, to the second.
Nothing fails; the dashboards render a confident wrong answer.

The schema comment above justifies the current shape as storage economy — "a
repository has one contributor locally, so repeating it per row would be
storage spent on a constant". That reasoning is correct **locally** and false
in the shared database the sidecar exists to populate. This record revises it.

## Decision

**The primary key on `whodunit_repos` becomes `(repo_id, contributor)`.**

`contributor` stays `VARCHAR(320)` holding the git committer email as written.
It is not hashed, not truncated, and not replaced by a surrogate id.

The other three tables carry `contributor` as a column, already decided in
WHO-170 and WHO-192. This record covers only the key.

## Byte width, measured

MySQL's InnoDB index limit is 3072 bytes, and `utf8mb4` costs 4 bytes per
character at the index level regardless of the bytes actually stored.

| column | declared | index cost |
| --- | --- | --- |
| `repo_id` | `VARCHAR(64)` | 256 bytes |
| `contributor` | `VARCHAR(320)` | 1280 bytes |
| **composite key** | | **1536 bytes** |
| **InnoDB cap** | | **3072 bytes** |

**It fits, with 1536 bytes to spare.** This was the main argument for hashing
the email into a fixed-width surrogate, and the measurement removes it. 320 is
itself the RFC 5321 maximum for an address, so this is the worst case rather
than a typical one.

Confirmed against the engine rather than only on paper — MySQL 8 accepts the
composite key under `utf8mb4` without complaint:

```sql
CREATE TABLE t (
  repo_id     VARCHAR(64)  NOT NULL,
  contributor VARCHAR(320) NOT NULL DEFAULT '',
  PRIMARY KEY (repo_id, contributor)
) DEFAULT CHARSET=utf8mb4;
-- accepted; utf8mb4_0900_ai_ci
```

## Idempotency under the new key

Re-syncing the same data stays a no-op, and the reason is worth stating because
the key change alters what "the same data" means.

Today the conflict target is `(repo_id)`; it becomes `(repo_id, contributor)`.
A second sync from the *same* person hits the same row and updates it in place,
exactly as before. A sync from a *different* person now inserts a second row
instead of overwriting the first — which is the entire point.

The `ON CONFLICT` / `ON DUPLICATE KEY` clauses need their target widened to
match. Every other upsert is unaffected: `whodunit_commits` is already keyed
`(commit_sha, repo_id)`, and a commit SHA is globally unique, so two people
syncing the same commit converge on one row rather than duplicating it. That is
correct — a commit has one attribution regardless of who pushed it.

## Rejected alternatives

**A hashed contributor id.** Replace the email with, say, the first 16 bytes of
its SHA-256, giving a fixed-width key. Rejected: the byte measurement above
shows there is no width problem to solve, and a hash makes every debugging
session and every support question harder — a row you cannot read is a row you
cannot check. It would also not be a privacy gain, since `whodunit_commits`
joins to git, where the email is in every commit object anyway.

**A surrogate integer id with a contributors lookup table.** Rejected: it
requires the sidecar to allocate ids, which means a read-modify-write against a
shared table on every sync, and two people syncing concurrently would race for
the same new id. The sidecar's writes are currently a single idempotent
transaction with no allocation step, and that property is worth more than the
bytes saved.

**Keep `repo_id` alone and disambiguate in the dashboards.** Rejected: the data
is already lost by then. The overwrite happens at write time, so no query can
recover which contributor a pre-overwrite row belonged to.

**Namespace `repo_id` itself** — hash the root SHA together with the
contributor. Rejected: it breaks the join to DevLake's `repo_commits`, which
matches on the real SHA, and it hides the identity in a field documented as
revealing neither name nor remote.

## Migration

SQLite cannot alter a primary key in place. The path is the standard rebuild:
create the new table under a temporary name, copy the rows, drop the original,
rename. MySQL can do it with a single `ALTER TABLE`, but the rebuild path works
on both and the sidecar targets both, so one implementation covers it.

**The rebuild does not belong in `Migrations`.** That list is documented as
best-effort `ADD COLUMN` statements whose expected failure is "column already
exists" (`internal/sidecar/schema.go:275-289`). Every statement in it is safe to
re-run and safe to fail. A table rebuild is neither: a half-completed rebuild
loses rows, and re-running one that already succeeded would copy from a table
that no longer exists. Putting it there would break the assumption every other
entry relies on.

It needs a **versioned migration mechanism** that records what has run and does
not re-run it. `SchemaVersion` is bumped from 1 to 2, and the rebuild is gated
on the stored version rather than attempted every sync.

**Safety.** The rebuild is destructive, so a verified backup precedes it and the
migration **fails closed** if the backup cannot be taken or cannot be verified —
it must not proceed on the assumption that the copy worked (WHO-177). Failing
closed here means the sync does not run, which is the one place in this codebase
where blocking is correct: everywhere else attribution degrades rather than
failing, but a destructive rebuild without a backup risks the data itself.

## Existing rows

An install that already has data has exactly one contributor per `repo_id` —
whoever last overwrote it. Those rows migrate as-is: the existing `contributor`
value becomes part of the key, and the row is preserved.

**What cannot be recovered is who the row belonged to before an overwrite.**
That history is gone, and this migration does not invent it. On a
single-contributor install nothing was ever lost. On a shared one, the rows
present at migration time are attributed to the last writer, and correct
attribution begins from the migration forward.

This is stated rather than papered over because a reader comparing pre- and
post-migration numbers on a shared database will see the split appear, and
should know it reflects when the fix landed rather than a change in who was
working.
