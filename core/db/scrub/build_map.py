#!/usr/bin/env python3
"""Build the scrub replacement map for the carried migration set.

Implements HH-3.1-scrub-rules.md mechanically. Enumeration inputs
arrive as config files (one literal per line, '#' starts a comment);
classification, exclusions, placeholder generation, and collision
checks happen here. The lane derives the candidate literals from the
carried files; this script applies the rules to them.

Exclusions applied, per the rules file: the business name in
identifying use (never enumerated; an exact-match candidate is dropped
as a guard, while customer names merely embedding it stay in the set),
reserved-domain logins and the persona names those logins spell,
gate-suite fixture identifiers, supplier names (their email addresses
still go, via the email rule), and addresses on owned domains. Every
other email domain is in the set per rule 7; rule 8's stop is a human
act (maintaining the domain lists), not a script behavior.

Replacements are patterned, obviously synthetic placeholders per rule
5, collision-checked against the text of both repositories. The T7
token is included or excluded by flag (default included); the flag
changes nothing else.

The map is written with mode 0600. It is never committed, never
reported, and deleted once prove.py's proofs pass. stdout reports
counts by rule class only and never prints a literal value.
"""

import argparse
import json
import os
import re
from collections import Counter

EMAIL_RE = re.compile(r"[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})")
MIGRATION_RE = re.compile(r"^\d{4}_.+\.sql$")
MAX_TEXT_FILE = 5_000_000

PLACEHOLDER_PATTERNS = {
    "name": "Scrubbed Party %04d",
    "first_name": "Scrubname%04d",
    "email": "scrubbed-%04d@example.invalid",
    "street_address": "%04d Placeholder Way",
    "attribution": "speaker-%04d",
}


def read_lines(path):
    """One literal per line; blank lines and '#' comments are skipped."""
    out = []
    if path is None:
        return out
    with open(path, "r", encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if line and not line.startswith("#"):
                out.append(line)
    return out


def norm(value):
    return re.sub(r"\s+", " ", value.strip().lower())


def iter_text_files(root, skip=(".git",)):
    """Yield (path, bytes) for plausible text files under root."""
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in skip]
        for name in sorted(filenames):
            path = os.path.join(dirpath, name)
            try:
                if os.path.getsize(path) > MAX_TEXT_FILE:
                    continue
                with open(path, "rb") as fh:
                    data = fh.read()
            except OSError:
                continue
            if b"\x00" in data:
                continue
            yield path, data


def carried_texts(carried_dir):
    texts = []
    for name in sorted(os.listdir(carried_dir)):
        if not MIGRATION_RE.match(name):
            continue
        path = os.path.join(carried_dir, name)
        with open(path, "r", encoding="utf-8") as fh:
            texts.append((name, fh.read()))
    return texts


def count_occurrences(text, literal, match):
    """Count occurrences; 'word' is whole-word and case-sensitive (rule 3)."""
    if match == "word":
        return len(re.findall(r"\b%s\b" % re.escape(literal), text))
    return text.count(literal)


def replace_occurrences(text, literal, replacement, match):
    if match == "word":
        return re.sub(
            r"\b%s\b" % re.escape(literal), lambda _m: replacement, text
        )
    return text.replace(literal, replacement)


def classify_email(address, owned, reserved):
    """Return an exclusion reason, or None when the address is in the set."""
    domain = address.rsplit("@", 1)[1].lower()
    if domain in owned:
        return "owned_domain"
    if domain in reserved:
        return "reserved_domain"
    return None


def spelled_forms(local_part):
    """Name forms a reserved-domain login spells (rule 2's persona test)."""
    parts = [p for p in re.split(r"[._%+-]+", local_part) if p]
    forms = {p.lower() for p in parts}
    if len(parts) > 1:
        forms.add(" ".join(p.lower() for p in parts))
    return forms


def enumerate_candidates(texts, cfg):
    """Build the candidate list and the exclusion counts, per the rules."""
    candidates = []
    excluded = Counter()
    owned = {d.lower() for d in cfg["owned_domains"]}
    reserved = {d.lower() for d in cfg["reserved_domains"]}
    business = {norm(b) for b in cfg["business_names"]}
    supplier = {norm(s) for s in cfg["supplier_names"]}
    fixtures = set(cfg["fixture_ids"])

    emails = {}
    for _name, text in texts:
        for match in EMAIL_RE.finditer(text):
            emails.setdefault(match.group(0), None)
    reserved_forms = set()
    for address in emails:
        reason = classify_email(address, owned, reserved)
        if reason is None:
            candidates.append(("email", address, "literal"))
        else:
            excluded[reason] += 1
            if reason == "reserved_domain":
                reserved_forms |= spelled_forms(address.split("@", 1)[0])

    def name_candidate(klass, literal, match):
        key = norm(literal)
        if key in business:
            excluded["business_name"] += 1
        elif key in supplier:
            excluded["supplier_name"] += 1
        elif literal in fixtures:
            excluded["fixture_identifier"] += 1
        elif key in reserved_forms:
            excluded["persona_of_reserved_login"] += 1
        else:
            candidates.append((klass, literal, match))

    for literal in cfg["names"]:
        name_candidate("name", literal, "literal")
    for literal in cfg["first_names"]:
        name_candidate("first_name", literal, "word")
    if cfg.get("t7") == "include" and cfg.get("t7_token"):
        name_candidate("first_name", cfg["t7_token"], "word")
    for literal in cfg["addresses"]:
        candidates.append(("street_address", literal, "literal"))
    for literal in cfg["attributions"]:
        candidates.append(("attribution", literal, "literal"))
    return candidates, excluded


def generate_placeholders(candidates, texts, corpus):
    """Assign a collision-free patterned placeholder to each candidate."""
    counters = Counter()
    taken = set()
    collisions = 0
    dropped = Counter()
    entries = []
    for klass, literal, match in candidates:
        occurrences = sum(
            count_occurrences(text, literal, match) for _n, text in texts
        )
        if occurrences == 0:
            dropped["no_occurrence:" + klass] += 1
            continue
        while True:
            counters[klass] += 1
            placeholder = PLACEHOLDER_PATTERNS[klass] % counters[klass]
            if placeholder in taken:
                continue
            encoded = placeholder.encode("utf-8")
            if any(encoded in data for _p, data in corpus):
                collisions += 1
                continue
            break
        taken.add(placeholder)
        entries.append(
            {
                "class": klass,
                "match": match,
                "original": literal,
                "replacement": placeholder,
                "occurrences": occurrences,
            }
        )
    return entries, collisions, dropped


def write_map(path, payload):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, sort_keys=True)
        fh.write("\n")
    os.chmod(path, 0o600)


def load_map(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def build(args):
    cfg = {
        "names": read_lines(args.names),
        "first_names": read_lines(args.first_names),
        "addresses": read_lines(args.addresses),
        "attributions": read_lines(args.attributions),
        "owned_domains": read_lines(args.owned_domains),
        "reserved_domains": read_lines(args.reserved_domains),
        "business_names": read_lines(args.business_names),
        "supplier_names": read_lines(args.supplier_names),
        "fixture_ids": read_lines(args.fixture_ids),
        "t7": args.t7,
        "t7_token": (read_lines(args.t7_token) or [None])[0],
    }
    texts = carried_texts(args.carried)
    candidates, excluded = enumerate_candidates(texts, cfg)
    corpus = list(iter_text_files(args.erp_root)) + list(
        iter_text_files(args.hh_root)
    )
    entries, collisions, dropped = generate_placeholders(candidates, texts, corpus)

    by_class = Counter(entry["class"] for entry in entries)
    payload = {
        "version": 1,
        "entries": entries,
        "stats": {
            "mapped": dict(sorted(by_class.items())),
            "excluded": dict(sorted(excluded.items())),
            "dropped": dict(sorted(dropped.items())),
            "collisions_resolved": collisions,
        },
    }
    write_map(args.map, payload)

    # Rule 8: a domain the lane cannot classify is reported by domain, never
    # by address, and the lane stops. Print the scrub-set domains so the
    # runner reviews them against the owned and reserved lists.
    owned = {d.lower() for d in cfg["owned_domains"]}
    reserved = {d.lower() for d in cfg["reserved_domains"]}
    seen = sorted(
        {m.group(1).lower() for _n, text in texts for m in EMAIL_RE.finditer(text)}
    )
    in_set = [d for d in seen if d not in owned and d not in reserved]
    owned_seen = [d for d in seen if d in owned]
    reserved_seen = [d for d in seen if d in reserved]
    print(
        "domains seen: %d (owned %d, reserved %d, in the scrub set %d)"
        % (len(seen), len(owned_seen), len(reserved_seen), len(in_set))
    )
    for domain in in_set:
        print("domain in set: %s" % domain)

    for klass in sorted(by_class):
        print("mapped %s: %d" % (klass, by_class[klass]))
    for reason in sorted(excluded):
        print("excluded %s: %d" % (reason, excluded[reason]))
    for reason in sorted(dropped):
        print("dropped %s: %d" % (reason, dropped[reason]))
    print("placeholder collisions resolved: %d" % collisions)
    print(
        "map written to %s (mode 0600; never commit; delete after proofs pass)"
        % args.map
    )
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Build the scrub replacement map (counts only on stdout)."
    )
    parser.add_argument("--carried", required=True,
                        help="carried migrations directory (core/db/migrations)")
    parser.add_argument("--erp-root", required=True,
                        help="ERP reference checkout, for collision checks")
    parser.add_argument("--hh-root", required=True,
                        help="hh repository root, for collision checks")
    parser.add_argument("--map", default="./scrub-map.json",
                        help="map output path (local only, mode 0600)")
    parser.add_argument("--names", help="customer and individual name literals")
    parser.add_argument("--first-names",
                        help="standalone first names (whole word, case sensitive)")
    parser.add_argument("--addresses", help="street address literals")
    parser.add_argument("--attributions",
                        help="speaker tokens to scrub from comments")
    parser.add_argument("--owned-domains", help="owned domains (rule 6)")
    parser.add_argument("--reserved-domains",
                        help="reserved test domains (logins excluded)")
    parser.add_argument("--business-names",
                        help="business name forms (identifying use only)")
    parser.add_argument("--supplier-names",
                        help="supplier names kept per rule 4")
    parser.add_argument("--fixture-ids",
                        help="gate-suite fixture identifiers kept per D32")
    parser.add_argument("--t7", choices=["include", "exclude"], default="include",
                        help="include or exclude the T7 token (default include)")
    parser.add_argument("--t7-token", help="file holding the T7 token, one line")
    args = parser.parse_args(argv)
    return build(args)


if __name__ == "__main__":
    raise SystemExit(main())
