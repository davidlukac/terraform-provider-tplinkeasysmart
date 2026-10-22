#!/usr/bin/env python3
"""Enforce the coverage policy on a Go cover profile.

Policy: every statement of the module is covered, except inside functions whose
doc comment contains `coverage: ignore - <reason>`. The reason is mandatory.
Test files are not measured, and neither is test-support code (NOT_MEASURED): the
fake switch in escptest is exercised by the library's own tests, so a branch of it
that no test reaches is a gap in that fake, not in the product.

Usage: covercheck.py cover.out [module-root]   (exit 1 when the policy is violated)
"""
import collections
import os
import re
import sys

MODULE = "github.com/davidlukac/terraform-provider-tplinkeasysmart/"
# Path prefixes (relative to the module root) of test-support packages. Their statements
# are dropped from the profile, so neither the policy nor the badge counts them.
NOT_MEASURED = ("internal/escp/escptest/",)
MARKER = re.compile(r"^\s*//\s*coverage:\s*ignore\b(?P<rest>.*)$")
BLOCK = re.compile(r"^(?P<file>.+):(?P<sl>\d+)\.\d+,(?P<el>\d+)\.\d+ (?P<n>\d+) (?P<count>\d+)$")


def parse_profile(text):
    """Return {(file, start, end): (statements, hits)}; duplicate blocks from several test binaries are merged."""
    blocks = {}
    for line in text.splitlines()[1:]:
        m = BLOCK.match(line)
        if not m:
            raise ValueError("unparseable cover profile line: " + line)
        name = m["file"].removeprefix(MODULE)
        if name.startswith(NOT_MEASURED):
            continue
        key = (name, int(m["sl"]), int(m["el"]))
        n, hits = blocks.get(key, (int(m["n"]), 0))
        blocks[key] = (n, hits + int(m["count"]))
    return blocks


def marked_functions(path):
    """Return [(start, end, reason)] for functions with a `coverage: ignore` doc-comment line.

    gofmt'd code is assumed: a function starts with `func` in column 0 and ends with `}` in column 0.
    """
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()
    found, problems = [], []
    for i, line in enumerate(lines):
        if not line.startswith("func "):
            continue
        j = i - 1
        reason = None
        while j >= 0 and lines[j].startswith("//"):
            m = MARKER.match(lines[j])
            if m:
                rest = m["rest"].strip()
                if not rest.startswith("-") or not rest.lstrip("- ").strip():
                    problems.append(f"{path}:{j + 1}: 'coverage: ignore' needs a reason: // coverage: ignore - <reason>")
                reason = rest.lstrip("- ").strip()
            j -= 1
        if reason is None:
            continue
        end = next((k for k in range(i, len(lines)) if lines[k] == "}"), len(lines) - 1)
        found.append((i + 1, end + 1, reason))
    return found, problems


def evaluate(blocks, root):
    """Classify blocks. Returns (total, covered, excluded, uncovered, problems)."""
    marks, problems = {}, []
    for f in {k[0] for k in blocks}:
        path = os.path.join(root, f)
        if os.path.exists(path):
            marks[f], p = marked_functions(path)
            problems += p
        else:
            marks[f] = []
    total = covered = excluded = 0
    uncovered, exclusions = [], collections.OrderedDict()
    for (f, sl, el), (n, hits) in sorted(blocks.items()):
        total += n
        if hits > 0:
            covered += n
            continue
        hit = next(((a, b, why) for a, b, why in marks[f] if a <= sl and el <= b), None)
        if hit:
            excluded += n
            exclusions[(f, hit[0], hit[2])] = exclusions.get((f, hit[0], hit[2]), 0) + n
        else:
            uncovered.append((f, sl, el, n))
    return total, covered, excluded, uncovered, exclusions, problems


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    root = argv[2] if len(argv) > 2 else "."
    with open(argv[1], encoding="utf-8") as fh:
        profile = fh.read()
    total, covered, excluded, uncovered, exclusions, problems = evaluate(parse_profile(profile), root)
    measured = total - excluded
    pct = 100.0 * covered / measured if measured else 100.0
    print(f"statements: {total}  covered: {covered}  excluded: {excluded}  uncovered: {total - covered - excluded}")
    print(f"effective coverage (excluded statements not counted): {pct:.1f}%")
    for (f, line, why), n in exclusions.items():
        print(f"  excluded {n} statement(s) in {f}:{line} - {why}")
    for f, sl, el, n in uncovered:
        print(f"  UNCOVERED {f}:{sl}-{el} ({n} statement(s))")
    for p in problems:
        print("  PROBLEM " + p)
    return 1 if uncovered or problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
