# Field inventory

What each agent records on disk, which of it whodunit reads today, and what
is deliberately left alone.

Every entry was verified by reading real transcripts on this machine and
counting occurrences — not from vendor documentation, which describes intent
rather than what lands on disk. Counts are given so a field appearing in
40,000 records can be told apart from one appearing twice.

**Last surveyed: 2026-08-14.** Corpus: 1,862 Claude Code transcripts, 125
Codex rollouts, 8 agy conversation databases.

## Why this document exists

Two questions kept recurring and neither had a written answer: *are we
collecting everything we could?* and *why is this metric missing for that
agent?* Answering them from the adapter source is misleading, because the
adapter only shows what we take — not what was there to take.

It also exists because the survey that produced it found two live bugs. Both
were fields sitting in plain sight, indexed and populated, that the adapter
walked past while its own comments claimed the data did not exist.

## The rule everything here is measured against

**NAV-25**: the journal stores counts, file paths and line hashes. Never
prompt text, never file contents. A field is classified as:

- **SAFE** — a count, an enum, an identifier, a name. Storable.
- **RISKY** — may embed user intent or file content depending on the call.
  Countable, not storable.
- **FORBIDDEN** — is prompt text or file content. Never read except to
  count lines or compute a hash, and never retained.

The distinction matters more than it looks. A survey that greps for
`input_tokens` finds hits in all three agents — but in agy every hit is
Gemini API *documentation the agent was reading*, not telemetry. Content and
telemetry are not separable by pattern; they have to be separated by
reading.

---

## Claude Code

`~/.claude/projects/<slug>/<session-uuid>.jsonl` — one JSON object per line.

### Read today

| Field | Used for |
|---|---|
| `timestamp` | entry time, lookback window |
| `sessionId` | session grouping (hashed before it reaches a trailer) |
| `version` | agent version in the trailer |
| `message.content[].name` | tool name |
| `message.content[].input.file_path` | the file an edit touched |
| `message.content[].input.content` / `new_string` | line counts and line hashes only — never stored |
| `type` | user/assistant message counts |

### Present, not read

| Field | Count | Class | What it would give |
|---|---|---|---|
| `message.usage.input_tokens` | 43,873 turns | SAFE | real cost |
| `message.usage.output_tokens` | 43,873 | SAFE | real cost |
| `message.usage.cache_read_input_tokens` | 43,873 | SAFE | cache effectiveness |
| `message.usage.cache_creation_input_tokens` | 43,873 | SAFE | cost attribution |
| `message.model` | 43,873 | SAFE | per-model cost; 4 distinct |
| `gitBranch` | 85,295 | SAFE | attribution per branch; 52 distinct |
| `attributionMcpServer` | 8,820 | SAFE | per-server MCP use; 8 distinct |
| `attributionMcpTool` | 8,820 | SAFE | per-method MCP use; 44 distinct |
| `effort` | 40,556 | SAFE | reasoning tier; medium/high/xhigh |
| `permissionMode` | 1,974 | SAFE | autonomy granted; 5 values |
| `message.stop_reason` | 43,859 | SAFE | turns truncated vs completed |
| `type=system, subtype=api_error` | 1,132 | SAFE | session friction |
| `toolUseResult.userModified` | 3,298 | SAFE | **human edited the agent's output** |
| `toolUseResult.structuredPatch[].oldLines/newLines` | — | SAFE | change size without the change |
| `isSidechain` | 85,295 | SAFE | subagent vs main thread — always `false` here, so unverified |
| `type=system, subtype=compact_boundary` | 27 | SAFE | **whether a session was ever compacted** — 4 of 60 sessions |
| `cwd` | 81,644 | SAFE | already known from the repo |

**Token coverage is total**: 43,873 of 43,873 assistant turns carry `usage`.
Not sampled, not optional.

**Cache efficiency is a real lever, and it varies by model.** An earlier
draft of this document claimed caching was automatic and uniform, on the
strength of a 99% figure. That figure was wrong: it counted only
`input_tokens` as uncached and ignored `cache_creation_input_tokens`,
which is equally uncached on arrival and priced at 1.25x base. Measured
properly across 400 recent transcripts, 510 turns:

| model | turns | read ratio | write amortization |
|---|---|---|---|
| opus-4-8 | 235 | 54.3% | 4.95x |
| sonnet-5 | 231 | 41.5% | 2.58x |
| fable-5 | 40 | 48.9% | 21.03x |
| haiku-4-5 | 4 | 42.2% | **0.73x** |
| **total** | **510** | **47.6%** | **3.60x** |

Two derived metrics, both matching the names the Claude Console uses:

    read_ratio   = cache_read / (input + cache_write + cache_read)
    amortization = cache_read / cache_write

Amortization is the one that finds waste. A write costs 1.25x base and a
read costs 0.1x, so a write needs roughly 1.25x reads before it pays for
itself. Haiku above sits at 0.73x — those writes never paid back. The
same shape appears in the Console on a different model (Sonnet 4.6 at
0.0% read ratio over 125K input tokens), which is what makes this worth
reporting per model rather than as one number.

### How to present it

The Claude Console's own Caching page is the reference, and its panel set
maps onto Grafana with one substantive divergence:

| Console panel | Grafana equivalent | Note |
|---|---|---|
| three stat cards, sparkline + delta vs previous period | Stat, sparkline on | the delta matters — a ratio alone does not say whether it is improving |
| per-model breakdown, stacked composition bar per row | Table with a bar-gauge cell | **three** segments, not four (below) |
| input token composition over time, stacked area | Time series, stacked | |
| cache read ratio, line per model | Time series | |
| write amortization, line per model | Time series, log y axis | threshold at **1.25x**, not 1.00x |

Two things not to copy verbatim:

**The stacked bar has three segments here, not four.** The Console splits
cache writes into 5-minute and 1-hour TTL bands. The transcript records
`cache_creation_input_tokens` as a single scalar with no TTL, so that
split is unavailable — show `uncached / write / read` and say the TTL
band is not recorded, rather than rendering an empty 1h segment.

**Mark break-even at 1.25x.** The Console's amortization chart draws
gridlines at 0.50x/0.80x/1.00x and marks no break-even at all. A write
costs 1.25x base and a read 0.1x, so 1.00x is still a loss. A log y axis
is needed too: the measured spread runs 0.73x to 21.03x, and a linear
axis flattens the low end into the floor — which is the end that matters.

Context size is the other actionable signal: 92% of turns ran over 150k
tokens, median 472k, while only 4 of 60 sessions ever hit a compact
boundary. See NAV-104.

### Present, not to be read

`toolUseResult` contains `stdout`, `stderr`, `oldString`, `newString`,
`originalFile`, `content` — raw command output and file bodies. FORBIDDEN.
Its scalars (`userModified`, `interrupted`, `type`) are safe; the object as
a whole is not. `structuredPatch` carries `lines` alongside the line counts —
take the counts, drop the lines.

---

## Codex CLI

`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` — one JSON object per line,
every line `{timestamp, type, payload}`.

### Read today

`timestamp`, `type`, `payload.type`, `payload.name`, `payload.call_id`,
`payload.input` (apply_patch only, for counting), plus `session_meta.id`,
`.cwd`, `.cli_version`.

### The structural gap

`ParseSince` filters to `type == "response_item"`. That discards:

- **18,142 `event_msg` records** — where token counts, timing and MCP
  results live
- **4,089 `turn_context` records** — where the model, effort and approval
  policy live

**40% of every transcript is invisible to the adapter today.** Not a missing
field: a missing record type.

### Present, not read

| Field | Count | Class | What it would give |
|---|---|---|---|
| `event_msg/token_count.info.total_token_usage.*` | 10,215 | SAFE | cost, incl. `reasoning_output_tokens` |
| `token_count.info.model_context_window` | 10,215 | SAFE | context pressure |
| `token_count.rate_limits.*` | 10,215 | SAFE | throttling as a friction signal |
| `turn_context.model` | 4,089 | SAFE | per-model cost; 6 distinct |
| `turn_context.effort` | 2,548 | SAFE | reasoning tier |
| `turn_context.approval_policy` | 4,089 | SAFE | autonomy granted |
| `session_meta.git.branch` | 180 | SAFE | attribution per branch |
| `session_meta.git.commit_hash` | 180 | SAFE | ties a session to a commit directly |
| `session_meta.thread_source` | 165 | SAFE | **real subagent marker** — 6 of 125 |
| `session_meta.parent_thread_id` | 2 | SAFE | subagent lineage |
| `task_complete.duration_ms` | 202 | SAFE | **turn latency** — 411ms to 1.4M ms |
| `task_complete.time_to_first_token_ms` | 188 | SAFE | responsiveness |
| `mcp_tool_call_end.invocation.{server,tool}` | 130 | SAFE | MCP identity |
| `mcp_tool_call_end.duration` | 130 | SAFE | MCP latency |
| `function_call.namespace` | 243 | SAFE | **MCP calls we currently miss** |
| `*.status` (custom_tool_call, patch_apply_end) | 1,468 | SAFE | outcome precision |
| `turn_aborted.reason` | 19 | SAFE | interruptions |

Codex is the richest of the three: it is the only agent carrying per-turn
**timing**, and the only one separating **reasoning tokens**.

### Present, not to be read

FORBIDDEN: `agent_message.message`, `agent_reasoning.text`,
`user_message.message`, `message.content`, `reasoning.summary`,
`turn_context.user_instructions`, `developer_instructions`,
`base_instructions`, `patch_apply_end.unified_diff`,
`task_complete.last_agent_message`, `compacted.*`, `world_state.*`.

RISKY, and worth naming because they are tempting:
`mcp_tool_call_end.invocation.arguments` and `web_search_end.query` carry
user intent verbatim — one observed value was a search string naming an
unrelated private project. Count them; do not store them.

---

## Antigravity CLI (`agy`)

`~/.gemini/antigravity-cli/conversations/<uuid>.db` — SQLite, one database
per conversation, step payloads as protobuf with embedded JSON.

### Read today

`steps.idx`, `steps.step_type`, `steps.step_payload`. Three columns of one
table.

### Present, not read

| Source | Count | Class | What it would give |
|---|---|---|---|
| `steps.status` | 223 rows | ~~SAFE~~ **now read** | accepted / rejected / failed — 215 / 6 / 2 |
| `executor_metadata.data` → model | 21 rows | SAFE | model per turn; 2 distinct |
| `executor_metadata.data` → permission tokens | 8 dbs | SAFE | autonomy granted |
| `steps.permissions` | 13 rows | SAFE (identity half) | MCP server/tool, qualified |
| `steps.metadata` | 223 rows | mixed | cleaner JSON than the payload |
| `steps.error_details` | 7 rows | RISKY | failure reason, may quote a line |
| `trajectory_meta.cascade_id` | 8 | SAFE | conversation lineage |

`steps.status` was the notable one: indexed, populated on every row, and
ignored, while the adapter hardcoded `Outcome: "unknown"` and its own
comment stated agy records no rejection signal. **Fixed (NAV-84)** — it is
now read, and the eight non-success rows resolve into three distinct
meanings rather than one: six carry `error_details` beginning "Permission
denied" (a human declining, which is what an acceptance rate is measured
against), one "context cancelled", one nothing at all.

### Confirmed genuinely absent

Do not chase these — they were checked and are not there:

- **token usage** — every apparent hit is Gemini API documentation the agent
  was reading, in two steps of one database
- **finish/stop reason** — zero occurrences of any spelling, in any table
- **per-step timing** — no timestamp column; the adapter's fallback to file
  mtime is the only option
- **git branch** — the 21 `gitBranchName` hits are Linear's *suggested*
  branch inside MCP responses, not the checked-out branch
- **subagent marker** — the signals exist (`has_subtrajectory`,
  `parent_references`) and are zero and empty in all 8 databases

The 921 apparent "subagent" hits are system-prompt text describing a tool.
This is the clearest case of why greps do not settle these questions.

---

## Cross-agent summary

| Signal | Claude Code | Codex | agy |
|---|---|---|---|
| token usage | ✅ 100% of turns | ✅ + reasoning split | ❌ absent |
| model per turn | ✅ | ✅ | ✅ unread |
| git branch | ✅ | ✅ + commit | ❌ absent |
| turn timing | ❌ | ✅ | ❌ absent |
| subagent | ⚠️ present, always false | ✅ | ❌ absent |
| MCP identity | ✅ | ✅ both tagging forms (NAV-83) | ✅ unread |
| permission mode | ✅ | ✅ | ✅ unread |
| outcome | ✅ read | ✅ read | ✅ read (NAV-84) |

Any metric built on these has to state which agents can support it. A
dashboard panel showing cost is meaningful for Claude Code and Codex and
structurally empty for agy — and saying so is better than rendering a zero,
which reads as "this agent is free".

## Method

Each agent was surveyed by enumerating every distinct key across a broad
sample with occurrence counts, then classifying each unread field by reading
real values rather than by name. Where a field's meaning was ambiguous, the
value was inspected before classification.

Two claims in this document contradict comments in our own source. Both were
verified directly against the data before being written down.
