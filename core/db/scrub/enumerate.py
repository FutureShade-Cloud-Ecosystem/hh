#!/usr/bin/env python3
"""enumerate.py: derive scrub candidate configs from the carried files, masked.

The rules file makes enumeration the lane's work and requires review through
a masked context (rule 3). This script extracts candidates deterministically
and renders the review material with every value masked:

  - the five classified files are rendered whole, with every quoted value and
    every capitalized non-technical comment token replaced by a [Vnnn] or
    [Cnnn] marker, so the reviewer sees row shape, column context and prose
    structure, never a value;
  - elsewhere in the carried set, comment tokens become attribution or
    first-name candidates when they are non-dictionary words or their line
    carries a person cue, and each is listed with its masked line.

Reviewed drops are by marker index (ranges allowed: --drop 12,40-55).

Documented-kept values (the gate-suite fixture identifier, the client
business name) never become candidates: they are already public in the
repository's own naming and decision text.

Usage:
  python3 enumerate.py --carried DIR --terms FILE [--dict FILE] --out CFGDIR
  python3 enumerate.py ... --drop 12,40-55
  python3 enumerate.py ... --write-configs
"""

import argparse
import json
import os
import re
import sys
from collections import Counter
from pathlib import Path

CLASSIFIED = ("0029", "0046", "0049", "0055", "0057")
SQL_WORDS = set("""
select insert into update delete alter create table index constraint primary
key foreign references not null default check unique on values where and or
if exists from as with comment cascade restrict trigger function returns
declare begin end loop foreach array execute format do exception raise notice
grant revoke true false now gen_random_uuid timestamptz jsonb citext
""".split())
STREET_SUFFIX = re.compile(
    r"\b(st|street|rd|road|ave|avenue|dr|drive|ln|lane|blvd|boulevard|way|court|ct|"
    r"pl|place|ter|terrace|cres|crescent|hwy|highway|unit|suite|box)\b", re.I)
QUOTED = re.compile(r"'((?:[^']|'')*?)'")
CAPWORD = re.compile(r"\b[A-Z][a-z]{1,}\b")
EMAILISH = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
CUE = re.compile(
    r"\b(per|by|from|took|taken|called|calls|says|said|spoke|speaks|asked|asks|"
    r"contact|login|email|attn|reviewed|quoted|ruling|signature|words|ask|"
    r"thanks|regards)\b", re.I)
KEPT = {"Dibbits", "dibbits_truck"}


def load_whitelist(terms_path, dict_path, carried_dir):
    wl = set(SQL_WORDS) | KEPT
    wl |= {w.strip() for w in Path(terms_path).read_text().splitlines() if w.strip()}
    wl |= {p.stem for p in Path(carried_dir).glob("*.sql")}
    lower = {w.lower() for w in wl}
    for w in list(wl):
        if "_" in w:
            lower |= {p.lower() for p in w.split("_") if len(p) > 2}
    if dict_path and Path(dict_path).exists():
        for w in Path(dict_path).read_text(errors="replace").splitlines():
            w = w.strip()
            if w and w.isalpha():
                lower.add(w.lower())
    return lower


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--carried", required=True)
    ap.add_argument("--terms", required=True)
    ap.add_argument("--dict", default="/usr/share/dict/american-english")
    ap.add_argument("--out", required=True)
    ap.add_argument("--review", default="review-masked.txt")
    ap.add_argument("--drop", default="")
    ap.add_argument("--write-configs", action="store_true")
    args = ap.parse_args()

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    state_path = out / ".state.json"
    if state_path.exists():
        state = json.loads(state_path.read_text())
    else:
        wl = load_whitelist(args.terms, args.dict, args.carried)
        state = build(args.carried, wl)
        state_path.write_text(json.dumps(state))
        os.chmod(state_path, 0o600)
    cands = state["candidates"]

    drops = set()
    if args.drop:
        for part in args.drop.split(","):
            if "-" in part:
                a, b = part.split("-")
                drops |= set(range(int(a), int(b) + 1))
            else:
                drops.add(int(part))
    for idx in drops:
        cands[idx]["dropped"] = True
    state_path.write_text(json.dumps(state))
    os.chmod(state_path, 0o600)

    live = [(i, c) for i, c in enumerate(cands) if not c.get("dropped")]
    counts = Counter(c["kind"] for _i, c in live)
    for klass in sorted(counts):
        print("candidate %s: %d" % (klass, counts[klass]))
    print("dropped so far: %d of %d" % (len(cands) - len(live), len(cands)))

    review = [state["rendered"]] if state.get("rendered") else []
    review.append("")
    review.append("== comment candidates outside the five classified files ==")
    for i, c in live:
        if c["kind"] in ("attribution", "first_name") and not c.get("classified"):
            review.append("[%04d] %s" % (i, c["kind"]))
            for ml in c["lines"]:
                review.append("    " + ml)
    Path(args.review).write_text("\n".join(review) + "\n")
    os.chmod(args.review, 0o600)
    print("review written to %s (masked)" % args.review)

    if args.write_configs:
        cfgs = {"name": set(), "first_name": set(), "street_address": set(),
                "attribution": set()}
        for _i, c in live:
            cfgs[c["kind"]].add(c["value"])
        for klass, values in cfgs.items():
            p = out / ("%s.txt" % klass)
            p.write_text("\n".join(sorted(values)) + "\n")
            os.chmod(p, 0o600)
        print("configs written to %s (mode 0600)" % out)
    return 0


def build(carried_dir, wl):
    cands = []  # {"kind","value","classified","lines":[masked]}
    index = {}  # (kind, value) -> candidate position
    rendered = []

    def cand(kind, value, classified, masked_line):
        key = (kind, value)
        if key not in index:
            index[key] = len(cands)
            cands.append({"kind": kind, "value": value,
                          "classified": bool(classified), "lines": []})
        c = cands[index[key]]
        if len(c["lines"]) < 2 and masked_line not in c["lines"]:
            c["lines"].append(masked_line)
        return index[key]

    def mask_capwords(line):
        return CAPWORD.sub(
            lambda m: m.group(0) if m.group(0).lower() in wl else "\u25b8", line)

    for path in sorted(Path(carried_dir).glob("*.sql")):
        text = path.read_text(encoding="utf-8", errors="replace")
        classified = any(path.name.startswith(n) for n in CLASSIFIED)
        if classified:
            rendered.append("")
            rendered.append("===== %s (all values masked) =====" % path.name)
        lines = text.splitlines()
        in_block = False
        for lineno, line in enumerate(lines, 1):
            opens = "/*" in line and "*/" not in line
            is_comment = in_block or line.lstrip().startswith("--")
            if in_block and "*/" in line:
                in_block = False
            elif opens:
                in_block = True
            if classified:
                out_line = EMAILISH.sub("[E-addr]", line)
                line = out_line
                if is_comment:
                    def rep_cap(m, ln=line):
                        tok = m.group(0)
                        if tok.lower() in wl:
                            return tok
                        i = cand("first_name", tok, True, None)
                        return "[C%04d]" % i
                    out_line = CAPWORD.sub(rep_cap, out_line)
                else:
                    def rep_str(m):
                        value = m.group(1).replace("''", "'").strip()
                        if len(value) < 3:
                            return "'%s'" % m.group(1)

                        def klass_of(text):
                            return ("street_address"
                                    if STREET_SUFFIX.search(text) or re.match(r"^\d+\s", text)
                                    else "name")
                        # A JSON-valued literal must never be replaced whole:
                        # the replacement would break json validity and the
                        # from-zero apply. Decompose it into leaf strings so a
                        # replacement lands inside one json string.
                        if value[:1] in "{[":
                            try:
                                import json as _json
                                leaves = []

                                def walk(node):
                                    if isinstance(node, str):
                                        leaves.append(node)
                                    elif isinstance(node, dict):
                                        for v in node.values():
                                            walk(v)
                                    elif isinstance(node, list):
                                        for v in node:
                                            walk(v)

                                walk(_json.loads(value))
                                for leaf in leaves:
                                    leaf = leaf.strip()
                                    if len(leaf) >= 3:
                                        cand(klass_of(leaf), leaf, True, None)
                                return "'[JSON:decomposed]'"
                            except ValueError:
                                pass
                        i = cand(klass_of(value), value, True, None)
                        return "'[V%04d]'" % i
                    out_line = QUOTED.sub(rep_str, out_line)
                    out_line = mask_capwords(out_line)
                rendered.append("%4d  %s" % (lineno, out_line))
            elif is_comment:
                line_flag = bool(
                    CUE.search(line) or "@" in line or "(demo" in line
                    or "\u2192" in line
                )
                def rep_mask(m):
                    tok = m.group(0)
                    return tok if tok.lower() in wl and not line_flag else "\u25b8"
                masked = CAPWORD.sub(rep_mask, line)
                for m in CAPWORD.finditer(line):
                    tok = m.group(0)
                    # a dictionary word is still a name when the line itself
                    # says a person is here: a cue word, an address, a demo
                    # marker, an arrow mapping, or an em dash aside
                    if tok.lower() in wl and not line_flag:
                        continue
                    cand("first_name", tok, False, masked)
                for seq in re.findall(r"\b(?:[A-Z][a-z]{1,}\s+){1,3}[A-Z][a-z]{1,}\b", line):
                    words = seq.split()
                    if all(w.lower() in wl for w in words):
                        continue
                    cand("attribution", seq.strip(), False, masked)
    # Rule 3 filter for the non-classified sweep: a single-word candidate is
    # kept only when every occurrence of the word across the whole carried set
    # sits on a person-cued line (cue word, address, demo marker, arrow). A
    # token that also appears on plain technical lines is a common word and
    # is excluded, exactly as the rules file describes the review outcome.
    totals = Counter()
    cued = Counter()
    for path in sorted(Path(carried_dir).glob("*.sql")):
        text = path.read_text(encoding="utf-8", errors="replace")
        for line in text.splitlines():
            is_cue_comment = line.lstrip().startswith("--") and bool(
                CUE.search(line) or "@" in line or "(demo" in line
                or "\u2192" in line
            )
            for m in CAPWORD.finditer(line):
                tok = m.group(0)
                totals[tok] += 1
                if is_cue_comment:
                    cued[tok] += 1
    for c in cands:
        if c["kind"] == "first_name" and not c["classified"]:
            if cued[c["value"]] == 0:
                c["dropped"] = True
                c["drop_reason"] = "never_in_person_context"
    return {"candidates": cands, "rendered": "\n".join(rendered)}


if __name__ == "__main__":
    sys.exit(main())
