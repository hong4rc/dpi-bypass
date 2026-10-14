#!/usr/bin/env python3
"""Point a Homebrew formula at a dpi-bypass release.

usage: bump-formula.py FORMULA TAG SHA256SUMS

Rewrites every release url to TAG and sets the sha256 on the line after each url
from SHA256SUMS.txt (the checksum file the release publishes).
"""
import re
import sys

formula, tag, sums_path = sys.argv[1:4]

sums = {}
for line in open(sums_path):
    parts = line.split()
    if len(parts) == 2:
        sums[parts[1].lstrip("*")] = parts[0]

url_re = re.compile(r'^(\s*url ")(https://github\.com/hong4rc/dpi-bypass/releases/download/)[^/]+/([^"]+)(")$')
sha_re = re.compile(r'^(\s*sha256 ")[0-9a-f]{64}(")$')

lines = open(formula).read().split("\n")
asset = None
bumped = 0
for i, line in enumerate(lines):
    m = url_re.match(line)
    if m:
        asset = m.group(3)
        if asset not in sums:
            sys.exit(f"{asset} is not in {sums_path}")
        lines[i] = f"{m.group(1)}{m.group(2)}{tag}/{asset}{m.group(4)}"
        continue
    m = sha_re.match(line)
    if m and asset:
        lines[i] = f"{m.group(1)}{sums[asset]}{m.group(2)}"
        asset = None
        bumped += 1

if bumped == 0:
    sys.exit("no url / sha256 pairs found; the formula layout changed")
open(formula, "w").write("\n".join(lines))
print(f"bumped {bumped} downloads to {tag}")
