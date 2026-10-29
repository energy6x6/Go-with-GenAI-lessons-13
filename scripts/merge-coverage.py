#!/usr/bin/env python3
"""Merge compatible atomic Go coverage profiles from unit and process tests."""

import argparse
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("profiles", nargs="+", type=Path)
parser.add_argument("--output", required=True, type=Path)
args = parser.parse_args()
blocks = {}
for profile in args.profiles:
    lines = profile.read_text().splitlines()
    if not lines or lines[0] != "mode: atomic":
        parser.error(f"{profile}: expected atomic coverage mode")
    for line in lines[1:]:
        location, statements, count = line.rsplit(" ", 2)
        statements, count = int(statements), int(count)
        previous = blocks.get(location)
        if previous is not None and previous[0] != statements:
            parser.error(f"{location}: source blocks differ; collect both reports from the same code")
        blocks[location] = (statements, count + (previous[1] if previous else 0))
args.output.parent.mkdir(parents=True, exist_ok=True)
args.output.write_text("mode: atomic\n" + "".join(f"{location} {statements} {count}\n" for location, (statements, count) in sorted(blocks.items())))
print(f"Merged {len(args.profiles)} profiles, {len(blocks)} source blocks -> {args.output}")
