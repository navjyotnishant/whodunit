# Linear → Jira key mapping

The backlog moved from Linear to Jira on 2026-08-29. Both use the `WHO-`
prefix, and **the numbers do not correspond**: Jira assigned keys by import
position, so Linear's `WHO-216` is Jira's `WHO-202`.

Commit messages written before the migration cite the Linear numbering.
`linear-to-jira.tsv` maps one to the other so those commits can resolve to
the issue they were actually about.

## Why this file exists rather than an offset

An offset looks like it would work and does not. Measured across all 207
matched issues:

| range | offset |
| --- | --- |
| Linear `WHO-124` … `WHO-221` | a clean `-14`, 98 consecutive keys |
| Linear `WHO-8` … `WHO-123` | **88 distinct offsets, from -100 to +96** |

Below `WHO-124` Jira's import order did not follow Linear's key order, so
arithmetic misattributes roughly 105 issues — silently, because the join
still succeeds and points at a real issue with a different meaning. That is
worse than no mapping: it looks like data.

## How it was built

Exact match on normalised title, both sides read from source: Linear's 221
issues from the DevLake `issues` table while it was still connected, Jira's
208 from the REST API. Nothing was mapped by arithmetic.

    total Linear      221
    total Jira        208
    unique matches    207
    ambiguous           0
    unmatched          14

The 14 unmatched are Linear's demo and onboarding issues — `[DEMO] …`,
"Get familiar with Linear", "Set up your teams" — which the migration
correctly dropped. Jira's 208th (`WHO-208`) was created after the migration
and has no Linear counterpart.

**One encoding trap worth recording:** the Linear export is not valid UTF-8.
Thirteen titles carry a cp1252 em dash (`0x97`), and without folding
`U+FFFD` / `—` / `–` together during normalisation, all thirteen — including
`WHO-216` — appear as false unmatches.

## Scope, and its boundary

This mapping covers the **207 migrated issues only**. Jira issues created
after the migration have keys above the imported range and no Linear
counterpart, so nothing should be mapped for them. A join through this file
must treat a missing key as *unmapped*, never as a near miss to be resolved
by arithmetic.

`build-linear-jira-map.py` regenerates it, but only while Linear's issues
are still in the lake. Once that connection is removed the source is gone
and this file is the record.
