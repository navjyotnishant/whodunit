#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-09-12
# Last updated: 2026-09-12
# Description: Self-check for build-catalog.py's WHO-248 parameter schema.

"""Assert-based checks against the real generated catalog.

    ./test_build_catalog.py
"""

import importlib.util
import json
import pathlib
import sys

_spec = importlib.util.spec_from_file_location(
    "build_catalog", pathlib.Path(__file__).parent / "build-catalog.py"
)
bc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bc)


def demo():
    text, skipped, literal_q = bc.render()
    payload = json.loads(text)
    metrics = {m["id"]: m for m in payload["metrics"]}

    # grain: custom variable, mysql-date-format, allowed set from the label:value
    # pairs, and "day" (a bare word, not a mask) must not be in it.
    grain_metric = next(
        m for m in metrics.values() if any(p["name"] == "grain" for p in m["params_schema"])
    )
    grain = next(p for p in grain_metric["params_schema"] if p["name"] == "grain")
    assert grain["format"] == "mysql-date-format", grain
    assert "%Y-%m-%d" in grain["allowed"], grain
    assert "day" not in grain["allowed"], grain

    # board: query variable with allValue=None -> required, no default, no
    # "__all__" silently accepted.
    board_metric = next(
        m for m in metrics.values() if any(p["name"] == "board" for p in m["params_schema"])
    )
    board = next(p for p in board_metric["params_schema"] if p["name"] == "board")
    assert board["required"] is True, board
    assert "default" not in board, board

    # evidence: custom variable with allValue="__all__" -> optional, default
    # "__all__", allowed includes both the enum values and the all-value.
    evidence_metric = next(
        m for m in metrics.values() if any(p["name"] == "evidence" for p in m["params_schema"])
    )
    evidence = next(p for p in evidence_metric["params_schema"] if p["name"] == "evidence")
    assert evidence["required"] is False, evidence
    assert evidence["default"] == "__all__", evidence
    assert "intersected" in evidence["allowed"], evidence
    assert "__all__" in evidence["allowed"], evidence

    # time_from_dt / time_from: co-required, iso-date format on both.
    dt_metric = next(
        (
            m
            for m in metrics.values()
            if any(p["name"] == "time_from_dt" for p in m["params_schema"])
        ),
        None,
    )
    if dt_metric is not None:
        tfd = next(p for p in dt_metric["params_schema"] if p["name"] == "time_from_dt")
        assert tfd["requires_with"] == ["time_to_dt"], tfd
        assert tfd["format"] == "iso-date", tfd

    # Deduplication: params_schema must have no repeated names even where the
    # positional params list does (adoption-rollout-coverage-style panels).
    for m in metrics.values():
        names = [p["name"] for p in m["params_schema"]]
        assert len(names) == len(set(names)), (m["id"], names)

    # params stays untouched — positional, still possibly duplicated.
    dup_metric = next(m for m in metrics.values() if len(m["params"]) != len(set(m["params"])))
    assert len(dup_metric["params_schema"]) < len(dup_metric["params"]), dup_metric["id"]

    # WHO-250: dimensions collects every query-type templating variable,
    # deduplicated by name, preferring the unfiltered variant over a
    # dashboard-specific filtered one.
    dims = payload["dimensions"]
    for name in ("board", "contributor", "team", "repo", "agent"):
        assert name in dims, (name, sorted(dims))
        assert dims[name]["label_column"] == "__text", dims[name]
        assert dims[name]["value_column"] == "__value", dims[name]
        assert "?" not in dims[name]["query"], "dimension queries take no bound params"
    assert "WHERE" not in dims["board"]["query"], (
        "expected board's unfiltered variant, got the exec-dashboard-filtered one",
        dims["board"],
    )

    # WHO-254: min_n is present only where the query's own threshold shape
    # (HAVING COUNT(...) >= N, or CASE WHEN COUNT(*) < N THEN NULL)
    # declares a minimum group size, grounded in the real SQL rather than
    # guessed.
    with_min_n = {m["id"]: m["min_n"] for m in metrics.values() if "min_n" in m}
    assert with_min_n.get("ai-attribution-evidence-agent-share-by-work-type") == 5, with_min_n
    for mid, n in with_min_n.items():
        sql = metrics[mid]["sql"]
        assert (
            f">= {n}" in sql or f">={n}" in sql or f"< {n} THEN NULL" in sql.upper()
        ), (mid, n, sql)
    # The CASE WHEN COUNT(*) < N THEN NULL shape — a row survives but the
    # computed value is nulled, distinct from HAVING's whole-row filtering.
    assert with_min_n.get("ai-impact-on-delivery-adoption-correlation") == 10, with_min_n
    # A metric with no threshold shape at all must carry no min_n — its
    # absence is "not detected", never a claimed zero.
    no_threshold = next(
        m for m in metrics.values()
        if "HAVING" not in m["sql"].upper() and "THEN NULL" not in m["sql"].upper()
    )
    assert "min_n" not in no_threshold, no_threshold["id"]

    # WHO-257 Stage 1: sql_hash/params_schema_hash present on every entry,
    # and the known duplicate pair (identical SQL, identical params) groups
    # together while a known false-positive candidate (same-sounding title,
    # different SQL/params) does not.
    for m in metrics.values():
        assert "sql_hash" in m and "params_schema_hash" in m, m["id"]

    def key(mid):
        m = metrics[mid]
        return (m["sql_hash"], m["params_schema_hash"])

    assert key("adoption-coverage") == key("adoption-rollout-coverage"), (
        "these two share byte-identical SQL and params — must group"
    )
    # ai-in-engineering-leadership-view's rework-rate variant adds
    # evidence/repo filters the others don't have — a real, different
    # contract, not a duplicate, even though the title sounds the same.
    if "ai-in-engineering-leadership-view-rework-rate-each-side" in metrics:
        assert key("ai-in-engineering-leadership-view-rework-rate-each-side") != key(
            "productivity-five-definitions-rework-rate-each-side"
        ), "different declared parameters must not be grouped as the same measure"

    dup_groups = {}
    for m in metrics.values():
        dup_groups.setdefault((m["sql_hash"], m["params_schema_hash"]), []).append(m["id"])
    duplicated = {k: v for k, v in dup_groups.items() if len(v) > 1}

    print(f"ok: {len(metrics)} metrics, params_schema present on all, {len(dims)} dimensions, {len(with_min_n)} with min_n, {len(dup_groups)} distinct measures ({len(duplicated)} duplicate groups), {len(skipped)} skipped panels")


if __name__ == "__main__":
    demo()
    sys.exit(0)
