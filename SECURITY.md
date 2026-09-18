# Security policy

## Reporting a vulnerability

Report privately through GitHub's
[private vulnerability reporting](https://github.com/navjyotnishant/whodunit/security/advisories/new)
— the **Report a vulnerability** button on the repository's Security tab.
That opens a thread only you and the maintainer can see, and it keeps the
report attached to the repository rather than to an inbox.

Please do not open a public issue for a suspected vulnerability. A public
issue is visible to everyone before there is a fix.

**What to include.** The version (`dun --version`), the operating system,
what you did, what happened, and what you expected instead. A minimal
reproduction is worth more than a long description. If you have a patch,
attach it to the report rather than opening a pull request, for the same
reason as above.

**What to expect.** This is a single-maintainer project, so the honest
commitment is narrow: an acknowledgement within a week, and an assessment
of whether it is a vulnerability and how severe once I have reproduced it.
I will tell you if it will take longer than that. There is no bounty.

Fixes ship in a normal release with a GitHub Security Advisory naming the
affected versions. If you would like credit, say so in the report; if you
would rather not be named, that is fine too.

## Supported versions

Only the latest release is supported. Fixes land on `main` and ship in the
next tag rather than being backported.

## What whodunit does, in security terms

Worth stating plainly, because it narrows what a vulnerability here can
even look like.

**Collection is local and makes no network calls.** The git hooks, the
daemon and `dun ingest` read local files — git, and the session
transcripts your coding agent already writes for its own purposes — and
write to a SQLite journal under `~/.whodunit`. Nothing is transmitted.

**One command sends data, and only when you configure it.** `dun sync`
publishes to a database you set with `dun config datalake` or pass with
`--to`. It is never run for you, and it does nothing until a target is
set. `dun sync --dry-run` prints the exact payload first.

**Prompt text and file contents are never stored.** This is enforced by a
test, not a policy: the journal's schema test fails the build if any
column name could hold message content, prompt text or a file body. A
field added later that would leak a prompt fails in CI.

**Local files are owner-only.** The journal, baseline snapshots and
configuration are written `0600` inside `0700` directories, and the
permissions are repaired on open if they were ever created more
permissively. They record which files you edited and when, which is
nobody else's business on a shared machine.

**Attribution reads; it does not execute.** Adapters parse an agent's
transcript. They never run anything from it, and they never write to an
agent's own files.

## What is in scope

- Anything that causes prompt text, message content or file contents to
  reach the journal, a trailer, or a sync payload.
- Anything that makes `dun sync` transmit to a target the user did not
  configure, or send more than
  [what is documented](README.md#what-dun-sync-sends).
- Credential handling — the datalake DSN, anything read from the
  environment, anything written to disk or a log.
- File permissions on anything under `~/.whodunit`.
- Path traversal or arbitrary writes through a crafted transcript,
  repository path or configuration value.
- Code execution triggered by parsing a transcript, a dashboard
  definition or a commit message.
- A git hook that can be made to run something other than `dun`.

## What is out of scope

- **The DevLake and Grafana deployment in `deploy/`.** It is a local
  development stack, and
  [its README says so](deploy/devlake/README.md): default credentials, no
  TLS, not to be put on a network anyone else can reach. Report issues
  upstream to those projects.
- **Anything requiring an attacker who already has your user account.**
  Someone with your shell can read `~/.whodunit` directly; that is the
  operating system's boundary, not this tool's.
- Vulnerabilities in the coding agents whose transcripts are read.
  Report those to the agent's vendor.
- Missing hardening with no exploit path, and automated scanner output
  without one.
- Denial of service against your own machine by pointing the tool at a
  pathological repository.

## A note on what the trailer records

`AI-Attribution` trailers are written into commit messages, and commit
messages are permanent in a way most storage is not — they travel with
every clone and survive a rewrite of everything around them. The trailer
carries an agent name, a version, a ratio, a model and an opaque session
token. It does not carry file paths, prompt text, or anything identifying
beyond the commit's own author.

If you believe a trailer can be made to carry more than that, it is in
scope and worth reporting.
