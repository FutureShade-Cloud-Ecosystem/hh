#!/usr/bin/env python3
"""Apply the scrub map to the carried files and run the proofs.

Proofs, per the rules file: zero remaining literal matches of any
mapped original (reported as a count, never as literals), placeholder
occurrence and collision checks, and byte-identity of filenames,
version numbers, and all code outside comments and strings against the
ERP source. The from-zero apply proof is delegated to the binary's
migrate role against a scratch database; see README. The ERP gate
suite is likewise run by the lane, not by this script.

stdout carries counts only, never a literal value. Exit status is zero
only when every proof this script runs passes.
"""

import argparse
import os
import re
import sys
from collections import Counter

from build_map import (
    MIGRATION_RE,
    count_occurrences,
    load_map,
    replace_occurrences,
)

VERSION_RE = re.compile(r"\b\d{4}\b")
CLASS_ORDER = {
    "email": 0,
    "street_address": 1,
    "name": 2,
    "attribution": 3,
    "first_name": 4,
}


def code_view(text):
    """Collapse comments and string literals to canonical markers.

    What remains is the code region: every string literal becomes '' and
    every comment vanishes, so two files with equal code views differ
    only inside comments and strings, regardless of how long those
    comments or strings are. Double-quoted identifiers are code and are
    kept. Dollar-quoted bodies are not special-cased (see the lane
    report's Gaps).
    """
    out = []
    i, n = 0, len(text)
    state = "code"
    while i < n:
        ch = text[i]
        nxt = text[i + 1] if i + 1 < n else ""
        if state == "code":
            if ch == "-" and nxt == "-":
                state = "line"
                i += 2
            elif ch == "/" and nxt == "*":
                state = "block"
                i += 2
            elif ch == "'":
                state = "string"
                out.append("''")
                i += 1
            else:
                out.append(ch)
                i += 1
        elif state == "line":
            if ch == "\n":
                state = "code"
                out.append("\n")
            i += 1
        elif state == "block":
            if ch == "*" and nxt == "/":
                state = "code"
                i += 2
            else:
                i += 1
        else:  # string, with '' as the escaped quote
            if ch == "'":
                if nxt == "'":
                    i += 2
                else:
                    state = "code"
                    i += 1
            else:
                i += 1
    return "".join(out)


def sql_texts(directory):
    texts = {}
    for name in sorted(os.listdir(directory)):
        if not MIGRATION_RE.match(name):
            continue
        with open(os.path.join(directory, name), "r", encoding="utf-8") as fh:
            texts[name] = fh.read()
    return texts


def apply_map(carried_dir, entries):
    """Replace every mapped original in the carried files, longest first."""
    ordered = sorted(
        entries, key=lambda e: (-len(e["original"]), CLASS_ORDER[e["class"]])
    )
    changed = 0
    for name, text in sql_texts(carried_dir).items():
        new = text
        for entry in ordered:
            new = replace_occurrences(
                new, entry["original"], entry["replacement"], entry["match"]
            )
        if new != text:
            path = os.path.join(carried_dir, name)
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(new)
            changed += 1
    return changed


def proof_remaining(texts, entries):
    """Occurrences of any mapped original still present; must be zero."""
    return sum(
        count_occurrences(text, entry["original"], entry["match"])
        for text in texts.values()
        for entry in entries
    )


def proof_placeholder_counts(texts, entries):
    """Entries whose placeholder count differs from the mapped count."""
    mismatches = 0
    for entry in entries:
        total = sum(text.count(entry["replacement"]) for text in texts.values())
        if total != entry["occurrences"]:
            mismatches += 1
    return mismatches


def proof_source_collisions(source_texts, entries):
    """Placeholders that appear anywhere in the ERP source; must be zero."""
    collisions = 0
    for entry in entries:
        if any(entry["replacement"] in text for text in source_texts.values()):
            collisions += 1
    return collisions


def proof_filenames(carried_texts, source_texts):
    return len(
        set(carried_texts) - set(source_texts)
    ) + len(set(source_texts) - set(carried_texts))


def proof_versions(carried_texts, source_texts):
    """Files whose code-region four-digit tokens changed; must be zero.

    Tokens are taken from the code view, so placeholders landing in
    strings or comments do not count as version-number changes.
    """
    mismatched = 0
    for name in set(carried_texts) & set(source_texts):
        if Counter(VERSION_RE.findall(code_view(carried_texts[name]))) != Counter(
            VERSION_RE.findall(code_view(source_texts[name]))
        ):
            mismatched += 1
    return mismatched


def proof_code_identical(carried_texts, source_texts):
    """Files whose code region differs from the ERP source; must be zero."""
    differ = 0
    for name in set(carried_texts) & set(source_texts):
        if code_view(carried_texts[name]) != code_view(source_texts[name]):
            differ += 1
    return differ


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Apply the scrub map and run the proofs (counts only)."
    )
    parser.add_argument("--carried", required=True,
                        help="carried migrations directory (core/db/migrations)")
    parser.add_argument("--source", required=True,
                        help="ERP source migrations directory at the pinned commit")
    parser.add_argument("--map", required=True, help="scrub map from build_map.py")
    parser.add_argument("--no-apply", action="store_true",
                        help="skip applying the map (already applied)")
    args = parser.parse_args(argv)

    entries = load_map(args.map)["entries"]
    if not args.no_apply:
        changed = apply_map(args.carried, entries)
        print("files changed by apply: %d" % changed)

    carried = sql_texts(args.carried)
    source = sql_texts(args.source)
    results = {
        "remaining original matches": proof_remaining(carried, entries),
        "placeholder occurrence mismatches": proof_placeholder_counts(
            carried, entries
        ),
        "placeholder collisions in ERP source": proof_source_collisions(
            source, entries
        ),
        "filename mismatches": proof_filenames(carried, source),
        "version-number mismatched files": proof_versions(carried, source),
        "code-region differing files": proof_code_identical(carried, source),
    }
    print(
        "from-zero apply: delegated (run the binary's migrate role against a"
        " scratch database; see README)"
    )
    for label, count in results.items():
        print("%s: %d" % (label, count))
    failures = sum(1 for count in results.values() if count != 0)
    print("proof failures: %d" % failures)
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
