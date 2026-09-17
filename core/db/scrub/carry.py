#!/usr/bin/env python3
"""Carry the ERP migration set into core/db/migrations, byte-verbatim.

Reads a pinned reference checkout of the ERP repository, verifies the
file count and the maximum migration number against expectations,
copies every migration byte-verbatim, and prints one line per file with
its git blob id (the sha1 of the source blob). The printed lines feed
core/db/PORTED.md, which records each carried file against its ERP
original so the tracking lane compares against the recorded original
rather than the scrubbed text.

Stdlib only. Requires the git binary for the pinned-commit check.
"""

import argparse
import hashlib
import os
import re
import subprocess
import sys

MIGRATION_RE = re.compile(r"^(\d{4})_.+\.sql$")


def git_blob_id(data: bytes) -> str:
    """Return the git blob id of data without invoking git."""
    return hashlib.sha1(b"blob %d\0" % len(data) + data).hexdigest()


def pinned_head(repo: str) -> str:
    proc = subprocess.run(
        ["git", "-C", repo, "rev-parse", "HEAD"],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        raise SystemExit(
            "carry: git rev-parse failed in %s: %s" % (repo, proc.stderr.strip())
        )
    return proc.stdout.strip()


def list_migrations(srcdir: str):
    entries = []
    for name in os.listdir(srcdir):
        match = MIGRATION_RE.match(name)
        if match and os.path.isfile(os.path.join(srcdir, name)):
            entries.append((int(match.group(1)), name))
    entries.sort()
    return entries


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Carry ERP migrations byte-verbatim from a pinned checkout."
    )
    parser.add_argument("--repo", required=True,
                        help="path of the pinned ERP reference checkout")
    parser.add_argument("--commit", required=True,
                        help="pinned commit the checkout must sit on")
    parser.add_argument("--source-subdir", default="backend/migrations",
                        help="migration directory inside the ERP checkout")
    parser.add_argument("--dest", required=True,
                        help="destination migrations directory")
    parser.add_argument("--expect-count", type=int, default=196,
                        help="expected number of migration files")
    parser.add_argument("--expect-max", type=int, default=212,
                        help="expected highest migration number")
    args = parser.parse_args(argv)

    head = pinned_head(args.repo)
    if head != args.commit:
        raise SystemExit(
            "carry: checkout is at %s, expected pinned commit %s" % (head, args.commit)
        )

    srcdir = os.path.join(args.repo, args.source_subdir)
    entries = list_migrations(srcdir)
    if len(entries) != args.expect_count:
        raise SystemExit(
            "carry: found %d migrations, expected %d" % (len(entries), args.expect_count)
        )
    top = entries[-1][0]
    if top != args.expect_max:
        raise SystemExit(
            "carry: max migration number is %04d, expected %04d" % (top, args.expect_max)
        )

    os.makedirs(args.dest, exist_ok=True)
    for _number, name in entries:
        src = os.path.join(srcdir, name)
        dst = os.path.join(args.dest, name)
        with open(src, "rb") as fh:
            data = fh.read()
        with open(dst, "wb") as fh:
            fh.write(data)
        with open(dst, "rb") as fh:
            verify = fh.read()
        if verify != data:
            raise SystemExit("carry: byte verification failed for %s" % name)
        print("%s %s" % (name, git_blob_id(data)))
    print(
        "carried %d files, max %04d, commit %s" % (len(entries), top, head),
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
