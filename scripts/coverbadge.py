#!/usr/bin/env python3
"""Turn a Go cover profile into a shields.io endpoint JSON (the coverage badge).

The number is the measured statement coverage of the whole module: covered
statements over all statements, with `coverage: ignore` exclusions counted as
NOT covered (unlike the policy check in covercheck.py, which does not count
them). Test files are not measured.

Usage: coverbadge.py cover.out > coverage.json
"""
import json
import sys

import covercheck


def measure(profile_text):
    """Return (covered statements, total statements)."""
    blocks = covercheck.parse_profile(profile_text)
    total = sum(n for n, _ in blocks.values())
    covered = sum(n for n, hits in blocks.values() if hits > 0)
    return covered, total


def colour(pct):
    for bound, name in ((95, "brightgreen"), (90, "green"), (80, "yellowgreen"), (70, "yellow"), (60, "orange")):
        if pct >= bound:
            return name
    return "red"


def badge(profile_text):
    covered, total = measure(profile_text)
    pct = 100.0 * covered / total if total else 0.0
    return {"schemaVersion": 1, "label": "coverage", "message": f"{pct:.1f}%", "color": colour(pct)}


def main(argv):
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    with open(argv[1], encoding="utf-8") as fh:
        print(json.dumps(badge(fh.read())))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
