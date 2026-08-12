#!/usr/bin/env python3
# Author: Navjyot Nishant
# Created: 2026-08-12
# Last updated: 2026-08-12
# Description: Turns the mounted dashboards into files any Grafana can import.

"""Generate importable copies of the dashboards in dashboards/.

The dashboards under dashboards/ are mounted straight into the bundled
DevLake Grafana, where the datasource is always named "mysql" — every stock
DevLake dashboard hardcodes that name, so ours do too. That makes them
useless to anyone with their own Grafana, where the datasource is called
something else and the import silently binds to nothing.

This produces the variant Grafana's own "Export for sharing externally"
produces: an __inputs block declaring a datasource requirement, and the
pinned name replaced by a placeholder the importer fills in.

Generated from the mounted files rather than maintained beside them. Two
hand-edited copies of a 22-panel dashboard drift within a month, and the
drift is invisible until someone imports the stale one.

    ./export-dashboards.py            # write dashboards-import/
    ./export-dashboards.py --check    # verify they are current (for CI)
"""

import argparse
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).parent
SOURCE = HERE / "dashboards"
# Deliberately NOT inside dashboards/. That directory is mounted into the
# bundled Grafana, whose provider scans it recursively — an export/ subfolder
# there gets provisioned as a second copy of every dashboard, with an
# unresolved ${DS_WHODUNIT} datasource that renders broken. Observed, not
# theorised: it produced six dashboards where there should be three.
OUTPUT = HERE / "dashboards-import"

# The name Grafana substitutes at import time. Its own exporter uses
# DS_<UPPERCASED DATASOURCE NAME>, and the import dialog shows the label.
INPUT_NAME = "DS_WHODUNIT"
INPUT_PLACEHOLDER = "${" + INPUT_NAME + "}"


def to_importable(dashboard: dict) -> dict:
    """Return dashboard with its datasource replaced by an input."""
    out = json.loads(json.dumps(dashboard))  # deep copy; the source is not touched

    out["__inputs"] = [
        {
            "name": INPUT_NAME,
            "label": "MySQL",
            "description": "The database holding the whodunit_* tables",
            "type": "datasource",
            "pluginId": "mysql",
            "pluginName": "MySQL",
        }
    ]

    # A dashboard imported into another Grafana must not collide with an
    # existing one, and must not claim an id from the exporting instance.
    out.pop("id", None)
    out["uid"] = ""

    for variable in out.get("templating", {}).get("list", []):
        if variable.get("type") == "datasource":
            # Unpin the name. Leaving `current` set to "mysql" makes the
            # import appear to succeed and then query a datasource that
            # does not exist on the importing instance.
            variable["current"] = {}
            continue
        # Query variables carry their own datasource reference.
        if variable.get("datasource"):
            variable["datasource"] = INPUT_PLACEHOLDER

    for panel in out.get("panels", []):
        _rewrite_panel(panel)

    return out


def _rewrite_panel(panel: dict) -> None:
    if panel.get("datasource") is not None:
        panel["datasource"] = INPUT_PLACEHOLDER
    for target in panel.get("targets", []):
        if target.get("datasource") is not None:
            target["datasource"] = INPUT_PLACEHOLDER
    # Row panels nest their children.
    for child in panel.get("panels", []):
        _rewrite_panel(child)


def render(path: pathlib.Path) -> str:
    dashboard = json.loads(path.read_text())
    return json.dumps(to_importable(dashboard), indent=2, ensure_ascii=False) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if the exported files are out of date, instead of writing them",
    )
    args = parser.parse_args()

    sources = sorted(SOURCE.glob("*.json"))
    if not sources:
        print(f"no dashboards found in {SOURCE}", file=sys.stderr)
        return 1

    stale = []
    for src in sources:
        want = render(src)
        dst = OUTPUT / src.name

        if args.check:
            if not dst.exists() or dst.read_text() != want:
                stale.append(dst.relative_to(HERE))
            continue

        OUTPUT.mkdir(exist_ok=True)
        dst.write_text(want)
        print(f"wrote {dst.relative_to(HERE)}")

    if stale:
        print(
            "these exported dashboards are out of date:\n  "
            + "\n  ".join(str(p) for p in stale)
            + "\n\nregenerate them with:  ./export-dashboards.py",
            file=sys.stderr,
        )
        return 1

    if args.check:
        print(f"{len(sources)} exported dashboard(s) are current")
    return 0


if __name__ == "__main__":
    sys.exit(main())
