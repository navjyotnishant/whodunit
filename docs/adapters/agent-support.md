# Agent support matrix

Which AI coding agents whodunit can attribute, how far each can be trusted,
and why.

Every row was verified by reading real transcripts on a real machine — not
from vendor documentation, which describes intent rather than what lands on
disk. Where something could not be verified, the row says so instead of
guessing.

**Last surveyed: 2026-08-13.**

For what each agent records on disk versus what whodunit reads — with counts,
privacy classification and the two fields we were ignoring — see
[field-inventory.md](field-inventory.md).

## The short version

| Agent | Ceiling | Status |
|---|---|---|
| [Claude Code](#claude-code) | `intersected` | Shipped |
| [Codex CLI](#codex-cli) | `intersected` | Shipped |
| [`agy` (Antigravity CLI)](#agy-antigravity-cli) | `intersected` | Shipped |
| [Cursor](#cursor) | unverified | Blocked — no edit sample |
| [Antigravity IDE](#antigravity-ide) | `inferred` | Bodies encrypted |
| [Gemini CLI](#gemini-cli) | unverified | Blocked — tier removed |
| [Gemini Code Assist](#gemini-code-assist) | — | Retired |
| [Windsurf](#windsurf) | `declared` | No local store found |
| [opencode](#opencode) | — | Not surveyed |

"Ceiling" is the strongest [confidence level](../../README.md) an adapter
could honestly reach, given what the agent records. It is a ceiling, not a
promise: an adapter still has to earn it.

## Buildable today

These three record the text the agent produced, so a line hash computed from
the transcript can be matched against what was actually staged. That is what
`intersected` means, and it is the only level that proves the agent's output
survived to the commit.

| | Claude Code | Codex CLI | `agy` |
|---|---|---|---|
| Store | `~/.claude/projects/<slug>/*.jsonl` | `~/.codex/sessions/Y/M/D/rollout-*.jsonl` | `~/.gemini/antigravity-cli/conversations/<uuid>.db` |
| Format | JSONL, append-only | JSONL, append-only | SQLite + protobuf blobs |
| Repo key | path slug (`/` → `-`) | `session_meta.cwd` | workspace URI in payload |
| Edit record | `tool_use` Edit/Write | `custom_tool_call` `apply_patch` | `replace_file_content`, `write_file` |
| Produced text | `content` / `new_string` | unified-diff `+` lines | `ReplacementContent` |
| Line numbers | derived | derived | given (`StartLine`/`EndLine`) |
| Call/result pairing | `tool_use_id` | `call_id` | step type 15 → 5/8/9/21 |
| Other tool calls | `tool_use.name` | call payload `name` | payload, after `call_<id>` |
| MCP calls | named — `mcp__<server>__<method>` | named as its own call type | none — shells out via `run_command` |
| Agent version | `version` field | `session_meta.cli_version` | not yet located |
| **Ceiling** | **`intersected`** | **`intersected`** | **`intersected`** |

### Claude Code

Shipped: [`internal/adapter/claudecode`](../../internal/adapter/claudecode).

Two-pass parse — a tool call and its result are separate records and the
result arrives later in the file, so outcomes are collected first and joined
by `tool_use_id`. Rejections are detectable, which is what makes an
acceptance rate possible (NAV-54).

### Codex CLI

Shipped: [`internal/adapter/codex`](../../internal/adapter/codex).

125 transcripts spanning February–August 2026 on the survey machine,
containing **1,625 `apply_patch` calls / 1,859 file operations** (1,674
update, 165 add, 20 delete). Running the adapter over all of them yields
exactly 1,859 entries — 1,792 accepted, 67 failed, 65,777 lines added,
51,692 line hashes — which is what confirms the parser sees everything the
survey counted.

Edits arrive as `custom_tool_call` with `name: "apply_patch"`, carrying a
unified diff:

```
*** Begin Patch
*** Update File: quick-start.sh
@@
-python -m pip install -r requirements.txt
+python -m pip install --disable-pip-version-check -r requirements.txt
*** End Patch
```

Paths in the patch are relative to `session_meta.cwd`, and are resolved to
absolute before being recorded — a relative path cannot be matched against a
staged diff.

**Finding a repository's sessions costs a read per transcript.** Codex files
sessions by date, not by repository, so the only way to know which
repository a session belongs to is the `session_meta` record inside it.
`SessionFiles` walks the tree and reads the first few lines of each file.
The alternative is an index whodunit would have to build and keep correct,
which is a worse trade at this scale.

**Deliberately out of scope:** roughly 6,400 `exec_command` shell calls
(`sed`, `cat`, `git`) and 62 heredoc writes. Those edit files too, but the
paths are buried in arbitrary shell strings. Regex-guessing at `sed -i`
fails silently and wrongly, which is worse than not attributing them at all.

> A first pass at this survey counted only `response_item:function_call`
> records, found 8 `apply_patch` calls, and concluded Codex was not worth
> adapting. `custom_tool_call` is a **separate record type** that was never
> inspected — the real figure is 200× higher. Enumerate record types before
> concluding anything about coverage.

**`~/.codex/history.jsonl` is not an integration point.** It stores raw user
prompt text, which the journal must never read. The rollout files already
give prompt counts without touching prose.

`codex exec --ephemeral` writes no transcript at all, so absence must stay
`undetermined`.

### `agy` (Antigravity CLI)

Shipped: [`internal/adapter/agy`](../../internal/adapter/agy).

Distinct from the Antigravity IDE despite sharing a parent directory — see
[below](#antigravity-ide).

One SQLite database per conversation. The `steps` table is one row per agent
action, and `step_payload` is **plaintext protobuf** (entropy 5.4–7.8,
46–86% printable), not encrypted:

```json
{"TargetFile": "/abs/path/calc.py",
 "StartLine": 1, "EndLine": 3,
 "TargetContent": "def add(a, b):\n    return a + b",
 "ReplacementContent": "def add(a, b):\n    return a + b\n\n\ndef multiply(a, b):\n    return a * b\n"}
```

Richer than the other two: the line range and the replaced text are given
outright, so added and removed counts need no diffing.

**Reading these databases requires the write-ahead log.** Opening with
`?immutable=1` returned **zero rows** from a conversation that plainly had
twelve steps — they were still in `-wal`. The adapter copies `.db`,
`.db-wal` and `.db-shm` to a temporary directory before reading: opening the
original read-write would let SQLite checkpoint and modify a user's file,
and opening it immutable silently under-reports a live session to nothing,
which looks like "no AI activity" rather than an error.

**Repository scoping is by edited path.** `agy` records no workspace
directory this adapter can trust — its conversations do not appear in
`conversation_summaries.db` (that index covers the IDE), and the `file:///`
URIs in its prose are embedded in prose. The absolute `TargetFile` on each
edit is reliable, so a conversation belongs to a repository when it edited a
file inside it.

**No accept/reject signal exists.** A declined call simply does not appear
in the store, so every entry is recorded `unknown` rather than `accepted` —
claiming acceptance would assert something the data does not say. `agy` is
therefore the one shipped adapter that cannot contribute to an acceptance
rate.

**A call and its result carry identical arguments**, so each edit appears
twice in `steps`. Entries are deduplicated on the produced text; counting
both would double every line the agent wrote.

**MCP calls are invisible, because `agy` does not make them.** Asked to read
a Linear issue with the Linear MCP server configured, it shelled out
instead: six `run_command` steps spawning
`npx -y mcp-remote https://mcp.linear.app/mcp` and speaking JSON-RPC over
stdio from a hand-written Node script.

So there is no MCP tool name to record — the tool `agy` used was
`run_command`, which is what the adapter stores. Per-server MCP usage is
therefore Claude Code only. Recovering it for `agy` would mean parsing shell
command lines, which is exactly the payload this package does not read
(NAV-25): a command line routinely carries file contents through a heredoc.

Observed 2026-08-13 with `mcp_config.json` already listing the server. This
may be a fallback rather than a design — `agy` had written that config
minutes earlier in the same session, and a restart may enable native MCP
calls. Worth re-checking before treating it as permanent.

## Blocked, and why

### Cursor

One store serves both the IDE and `cursor-agent`, so a single adapter would
cover both. Three locations, all readable:

| Path | Contents |
|---|---|
| `~/.cursor/chats/<workspace>/<session>/` | 738 sessions; `meta.json` carries `cwd`, `store.db` holds message blobs |
| `~/.cursor/ai-tracking/ai-code-tracking.db` | Cursor's own attribution tables |
| `…/Cursor/User/globalStorage/state.vscdb` | IDE state; stale, superseded |

Session blobs carry a `blobEncryptionKey` field but are **not encrypted** —
entropy 4.65–4.99 at 100% printable, parsing as plain JSON.

**Blocker:** no file-edit record exists in any of the 111 readable sessions.
They contain only `Read`, `Glob`, `Shell`, `Grep`, `WebSearch`, and a scan
for edit-shaped keys (`old_string`, `new_string`, `code_edit`,
`target_file`) returned nothing. An attempt to capture a fresh edit failed
on a usage limit. Until one real edit is captured, an adapter would be
inventing the format.

**Worth reading regardless:** `ai-code-tracking.db` shows Cursor solving the
same problem —

```sql
ai_code_hashes(hash, source, fileName, model, conversationId, …)
scored_commits(commitHash, linesAdded, tabLinesAdded, tabLinesDeleted,
               composerLinesAdded, composerLinesDeleted,
               humanLinesAdded, humanLinesDeleted, v1AiPercentage, …)
```

Content-hashing AI output and scoring commits into AI versus human lines is
independent validation of whodunit's approach. It also separates `tabLines`
(autocomplete) from `composerLines` (agent) — **a distinction this project's
spec does not yet make, and arguably should.** Empty on the survey machine,
so the schema is the finding, not the data.

### Antigravity IDE

`~/.gemini/antigravity/`, 2.0 GB on the survey machine.

Conversation bodies (`conversations/<uuid>.pb`, up to 27 MB) measure **8.00
bits/byte entropy with no compression magic and no extractable strings** —
encrypted at rest. `code_tracker/` beside them is plaintext, so the
encryption is deliberate for conversation bodies specifically.

Do not attempt to decrypt them. Working around a user's at-rest encryption
is not something this project should do to their own data, and a readable
index exists: `antigravity-cli/conversation_summaries.db` carries
`workspace_uris`, `step_count`, `agent_name` and timestamps, keyed by the
same `conversation_id` as the encrypted files.

**Ceiling `inferred`** — "this repo had Antigravity activity, and these files
appear in its tracker." Per-edit line counts and produced text are not
recoverable.

`code_tracker` blobs contain **full file contents**. Anything reading them
must extract paths and derived hashes only, and never write a file body to
the journal.

### Gemini CLI

`~/.gemini/tmp/<project-dir>/chats/session-*.jsonl`.

**Blocker:** cannot be run. Gemini Code Assist for individuals was removed
on 2026-06-18 and the CLI's free tier with it; `gemini` now fails with
`IneligibleTierError` before reaching any tool call. Quota remains for Code
Assist Standard and Enterprise, so this reopens on a licensed machine.

Two structural notes for whoever picks it up:

- **Project lookup is a file, not a hash.** `~/.gemini/projects.json` maps
  absolute path to directory basename. Records also carry `projectHash`,
  verified as `sha256(absolute_cwd)` with no trailing slash — useful as a
  tiebreak, since basenames collide across repos.
- **Snapshot, not append-only.** Each line is `{"$set": {"messages": […]}}`
  rewriting the entire array, so an incremental parse cannot assume new
  bytes mean new events. This breaks an assumption every other adapter
  shares.

### Gemini Code Assist

Retired. The VS Code extension refuses to sign in for individual accounts
and directs users to Antigravity. Its `globalStorage` directory does not
exist, so nothing is persisted to read. Not blocked — ended.

### Windsurf

No local store located during this survey. Declaration-only until one is
found.

### opencode

Not installed on the survey machine; `~/.local/share/opencode/storage/`
holds only `session_diff` JSON, no transcripts. Not surveyed.

## Platform coverage

**Every path in this document was observed on macOS.** Windows and Linux
locations are unverified, and at least one known bug is waiting there.

`os.UserHomeDir()` resolves correctly on all three platforms, so
`~/.claude/projects` and friends are not themselves the problem. What breaks
is everything built on top of a path.

### Known bug: the Claude Code slug is not portable

`SlugForCwd` encodes a working directory into a directory name by replacing
the path separator with `-`. Its comment says "every `/`", but the code
replaces `filepath.Separator`, which is `\` on Windows. Those disagree, and
the only test uses a Unix path — so it passes on macOS while proving nothing
about Windows.

Two things need checking on a Windows machine before any claim of support:

- **Which separator does Claude Code itself use?** It is a Node application,
  and Node paths are frequently `/`-normalised even on Windows. If Claude
  Code writes `C-Users-n-repo` and whodunit computes `C:-Users-n-repo`, the
  session directory is never found and attribution silently returns nothing.
- **What happens to the drive colon?** `C:` is not legal in a directory name
  on Windows, so Claude Code must encode it somehow. Nothing here knows how.

The failure mode is the dangerous one: no error, no transcripts found, and
every commit lands as `undetermined`. That reads as "no AI was used" rather
than "the adapter could not look."

### Locations to verify per platform

| Agent | macOS (verified) | Windows | Linux |
|---|---|---|---|
| Claude Code | `~/.claude/projects/` | `%USERPROFILE%\.claude\projects\`? | `~/.claude/projects/`? |
| Codex CLI | `~/.codex/sessions/` | `%USERPROFILE%\.codex\sessions\`? | `~/.codex/sessions/`? |
| `agy` | `~/.gemini/antigravity-cli/` | ? | ? |
| Cursor | `~/.cursor/chats/` + `~/Library/Application Support/Cursor/` | `%APPDATA%\Cursor\`? | `~/.config/Cursor/`? |
| Antigravity IDE | `~/.gemini/antigravity/` | ? | ? |

Question marks are conventions, not observations. Cursor is the one where
the app-support path definitely differs — macOS `~/Library/Application
Support`, Windows `%APPDATA%`, Linux `~/.config` — because that is a VS Code
convention rather than a dotfile.

Environment overrides already honoured: `CLAUDE_CONFIG_DIR` for Claude Code,
`XDG_DATA_HOME` and `WHODUNIT_HOME` for whodunit's own storage. These are
the escape hatch when a platform guess is wrong.

Until each row is verified on the platform in question, whodunit should be
described as **macOS-verified, Linux-likely, Windows-unverified** rather
than cross-platform.

## Why per-agent packages

The table is the argument. Three storage engines (JSONL, SQLite, protobuf),
three repo-keying schemes, three edit-record shapes — and Gemini's snapshot
model breaks the "new bytes mean new events" assumption the others share.

A single parser with per-agent branches would carry that divergence in its
control flow. Separate packages behind one interface keep each format's
weirdness contained, let a broken adapter degrade to `undetermined` instead
of failing ingest, give each agent its own golden fixtures for drift
detection, and let a contributor add an agent without touching shared
parsing.

## Keeping this current

This file is the single source of truth for agent support. It goes stale in
two ways:

1. **An adapter ships** — move it out of "buildable" into shipped, and link
   the package.
2. **A format changes underneath us** — which is what golden fixtures exist
   to catch. When a fixture breaks, the finding belongs here, not only in
   the commit message.

Re-survey when a new agent is targeted or a blocked one becomes testable.
Record what was actually observed, including counts, and say plainly when
something could not be verified.
