#!/usr/bin/env python3
"""Unit tests for the scrub and carry scripts, on synthetic fixtures only.

Every value here is invented and obviously synthetic: reserved domains,
patterned names, and a fixture street address. No real party appears.
"""

import json
import os
import stat
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_map
import carry
import prove

SEED_SQL = """-- Seed fixture. Reviewed per Speakerfixture One: the quoted ruling stays.
-- Contacts: testperson.alpha@live.example, ops@owned.example, fixture.user@example.com
CREATE TABLE party (id integer primary key, label text);
INSERT INTO party (id, label) VALUES
  (1, 'Testperson Alpha'),
  (2, 'Fixture Customer One'),
  (3, '100 Fixture Way'),
  (4, 'Keepsake Supplier Co');
"""


class CarryTests(unittest.TestCase):
    def test_git_blob_id_known_vectors(self):
        self.assertEqual(
            carry.git_blob_id(b""),
            "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
        )
        self.assertEqual(
            carry.git_blob_id(b"hello\n"),
            "ce013625030ba8dba906f756967f9e9ca394464a",
        )

    def test_list_migrations_filters_and_orders(self):
        with tempfile.TemporaryDirectory() as tmp:
            for name in ("0002_b.sql", "0010_j.sql", "0001_a.sql", "notes.txt"):
                with open(os.path.join(tmp, name), "w") as fh:
                    fh.write("select 1;\n")
            entries = carry.list_migrations(tmp)
        self.assertEqual(
            entries, [(1, "0001_a.sql"), (2, "0002_b.sql"), (10, "0010_j.sql")]
        )


class BuildMapTests(unittest.TestCase):
    def cfg(self, **overrides):
        base = {
            "names": [],
            "first_names": [],
            "addresses": [],
            "attributions": [],
            "owned_domains": [],
            "reserved_domains": [],
            "business_names": [],
            "supplier_names": [],
            "fixture_ids": [],
            "t7": "include",
            "t7_token": None,
        }
        base.update(overrides)
        return base

    def test_classify_email(self):
        owned = {"owned.example"}
        reserved = {"example.com"}
        self.assertIsNone(build_map.classify_email("a@live.example", owned, reserved))
        self.assertEqual(
            build_map.classify_email("ops@owned.example", owned, reserved),
            "owned_domain",
        )
        self.assertEqual(
            build_map.classify_email("fixture.user@example.com", owned, reserved),
            "reserved_domain",
        )

    def test_word_match_is_whole_word_and_case_sensitive(self):
        text = "Testname testname Testnamely Testname"
        self.assertEqual(build_map.count_occurrences(text, "Testname", "word"), 2)

    def test_persona_spelled_by_reserved_login_is_excluded(self):
        texts = [("0001_seed.sql", "login fixture.user@example.com\n'Fixture User'\n")]
        cfg = self.cfg(
            names=["Fixture User", "Other Person"],
            reserved_domains=["example.com"],
        )
        candidates, excluded = build_map.enumerate_candidates(texts, cfg)
        literals = {c[1] for c in candidates}
        self.assertNotIn("Fixture User", literals)
        self.assertIn("Other Person", literals)
        self.assertEqual(excluded["persona_of_reserved_login"], 1)
        self.assertEqual(excluded["reserved_domain"], 1)

    def test_supplier_name_kept_but_email_taken(self):
        texts = [("0001_seed.sql", "'Keepsake Supplier Co' sales@live.example")]
        cfg = self.cfg(names=["Keepsake Supplier Co"], supplier_names=["Keepsake Supplier Co"])
        candidates, excluded = build_map.enumerate_candidates(texts, cfg)
        kinds = {c[0] for c in candidates}
        self.assertEqual(kinds, {"email"})
        self.assertEqual(excluded["supplier_name"], 1)

    def test_business_name_exact_match_guarded(self):
        texts = [("0001_seed.sql", "'Client Business Co' 'Client Business Co Westside'")]
        cfg = self.cfg(
            names=["Client Business Co", "Client Business Co Westside"],
            business_names=["Client Business Co"],
        )
        candidates, excluded = build_map.enumerate_candidates(texts, cfg)
        literals = {c[1] for c in candidates}
        self.assertNotIn("Client Business Co", literals)
        self.assertIn("Client Business Co Westside", literals)
        self.assertEqual(excluded["business_name"], 1)

    def test_placeholder_collision_retries(self):
        candidates = [("name", "Fixture Customer One", "literal")]
        texts = [("0001_seed.sql", "'Fixture Customer One'")]
        corpus = [("x.txt", b"Scrubbed Party 0001 already here")]
        entries, collisions, _dropped = build_map.generate_placeholders(
            candidates, texts, corpus
        )
        self.assertEqual(collisions, 1)
        self.assertEqual(entries[0]["replacement"], "Scrubbed Party 0002")

    def test_t7_flag_changes_only_the_token(self):
        texts = [("0001_seed.sql", "'Tsevenname called about the fixture'")]
        included, _ = build_map.enumerate_candidates(
            texts, self.cfg(t7="include", t7_token="Tsevenname")
        )
        excluded, _ = build_map.enumerate_candidates(
            texts, self.cfg(t7="exclude", t7_token="Tsevenname")
        )
        self.assertEqual([c[1] for c in included], ["Tsevenname"])
        self.assertEqual(excluded, [])

    def test_write_map_mode_0600(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "map.json")
            build_map.write_map(path, {"version": 1, "entries": []})
            mode = stat.S_IMODE(os.stat(path).st_mode)
            loaded = build_map.load_map(path)
        self.assertEqual(mode, 0o600)
        self.assertEqual(loaded["version"], 1)


class ProveTests(unittest.TestCase):
    def test_code_view_strips_comments_and_strings(self):
        src = (
            "INSERT INTO t VALUES ('a -- b', 'c'); -- comment 'x'\n"
            "/* block 'y' */ CREATE"
        )
        view = prove.code_view(src)
        self.assertNotIn("a -- b", view)
        self.assertNotIn("comment", view)
        self.assertNotIn("block", view)
        self.assertIn("INSERT INTO t VALUES (", view)
        self.assertIn("CREATE", view)

    def make_tree(self, tmp):
        erp = os.path.join(tmp, "erp", "backend", "migrations")
        carried = os.path.join(tmp, "hh", "core", "db", "migrations")
        os.makedirs(erp)
        os.makedirs(carried)
        for directory in (erp, carried):
            with open(os.path.join(directory, "0001_seed.sql"), "w") as fh:
                fh.write(SEED_SQL)
        cfgdir = os.path.join(tmp, "cfg")
        os.makedirs(cfgdir)
        files = {
            "names.txt": "Fixture Customer One\nTestperson Alpha\n",
            "addresses.txt": "100 Fixture Way\n",
            "attributions.txt": "Speakerfixture One\n",
            "owned.txt": "owned.example\n",
            "reserved.txt": "example.com\n",
            "suppliers.txt": "Keepsake Supplier Co\n",
        }
        paths = {}
        for name, content in files.items():
            path = os.path.join(cfgdir, name)
            with open(path, "w") as fh:
                fh.write(content)
            paths[name] = path
        return erp, carried, paths

    def test_end_to_end_apply_and_proofs(self):
        with tempfile.TemporaryDirectory() as tmp:
            erp, carried, cfg = self.make_tree(tmp)
            map_path = os.path.join(tmp, "map.json")
            rc = build_map.main(
                [
                    "--carried", carried,
                    "--erp-root", os.path.join(tmp, "erp"),
                    "--hh-root", os.path.join(tmp, "hh"),
                    "--map", map_path,
                    "--names", cfg["names.txt"],
                    "--addresses", cfg["addresses.txt"],
                    "--attributions", cfg["attributions.txt"],
                    "--owned-domains", cfg["owned.txt"],
                    "--reserved-domains", cfg["reserved.txt"],
                    "--supplier-names", cfg["suppliers.txt"],
                ]
            )
            self.assertEqual(rc, 0)
            entries = build_map.load_map(map_path)["entries"]
            self.assertEqual(len(entries), 5)

            rc = prove.main(
                ["--carried", carried, "--source", erp, "--map", map_path]
            )
            self.assertEqual(rc, 0)

            scrubbed = prove.sql_texts(carried)["0001_seed.sql"]
            self.assertNotIn("Testperson Alpha", scrubbed)
            self.assertNotIn("testperson.alpha@live.example", scrubbed)
            self.assertIn("ops@owned.example", scrubbed)  # owned domain stays
            self.assertIn("fixture.user@example.com", scrubbed)  # reserved login stays
            self.assertIn("Keepsake Supplier Co", scrubbed)  # supplier name stays
            self.assertIn("CREATE TABLE party", scrubbed)  # code untouched

    def test_code_change_is_caught(self):
        with tempfile.TemporaryDirectory() as tmp:
            erp, carried, _cfg = self.make_tree(tmp)
            path = os.path.join(carried, "0001_seed.sql")
            with open(path) as fh:
                text = fh.read()
            with open(path, "w") as fh:
                fh.write(text.replace("CREATE TABLE party", "CREATE TABLE partx"))
            carried_texts = prove.sql_texts(carried)
            source_texts = prove.sql_texts(erp)
            self.assertEqual(prove.proof_code_identical(carried_texts, source_texts), 1)
            self.assertEqual(prove.proof_versions(carried_texts, source_texts), 0)


if __name__ == "__main__":
    unittest.main()
