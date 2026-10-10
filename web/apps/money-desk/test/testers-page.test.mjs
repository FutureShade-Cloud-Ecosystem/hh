// The testers' page held to its ledger and to the house rules. The page's claim ids and
// the ledger's keys must be the same set; every test proof must name a title that exists
// in the named spec file; every source proof must find its fragment word for word in
// the named file. The page itself carries no dash of the long kinds, no calendar date,
// no duration, no host name or address, no secret-shaped string, and the only person's
// name is the contact line.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { CLAIM_COVERAGE } from "./claims.ts";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const page = readFileSync(join(appDir, "public", "testers.html"), "utf8");

function titlesIn(file) {
  const source = readFileSync(join(appDir, file), "utf8");
  return new Set([...source.matchAll(/\btest\(\s*"((?:[^"\\]|\\.)*)"/g)].map((m) => m[1].replace(/\\"/g, '"')));
}

test("every claim on the page has a proof, and every proof is for a claim on the page", () => {
  const claims = new Set([...page.matchAll(/data-claim="([a-z0-9-]+)"/g)].map((m) => m[1]));
  const covered = new Set(Object.keys(CLAIM_COVERAGE));
  assert.deepEqual([...claims].filter((c) => !covered.has(c)), [], "the page claims what nothing proves");
  assert.deepEqual([...covered].filter((c) => !claims.has(c)), [], "the ledger covers a sentence the page no longer makes");
  assert.equal(claims.size, (page.match(/data-claim=/g) ?? []).length, "a claim id is used twice");
});

test("every proof names a test that exists or a source line that is there", () => {
  for (const [claim, proofs] of Object.entries(CLAIM_COVERAGE)) {
    assert.ok(proofs.length > 0, `${claim} names no proof`);
    for (const proof of proofs) {
      assert.ok(existsSync(join(appDir, proof.file)), `${claim}: ${proof.file} does not exist`);
      if ("test" in proof) {
        assert.ok(titlesIn(proof.file).has(proof.test), `${claim}: no test titled "${proof.test}" in ${proof.file}`);
      } else {
        assert.ok(readFileSync(join(appDir, proof.file), "utf8").includes(proof.source), `${claim}: ${proof.file} does not hold ${proof.source}`);
      }
    }
  }
});

test("the page keeps the house rules", () => {
  assert.ok(!/[–—]/.test(page), "no em or en dash");
  assert.ok(!/\b\d{4}-\d{2}-\d{2}\b/.test(page), "no calendar date");
  assert.ok(!/\b(January|February|March|April|May|June|July|August|September|October|November|December|Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)\b/.test(page), "no month or weekday");
  assert.ok(!/\b\d+\s*(seconds?|minutes?|hours?|days?|weeks?|months?|years?)\b/i.test(page), "no duration");
  assert.ok(!/https?:|\/\/|localhost|127\.0\.0\.1|\.(com|ai|net|io|org)\b/i.test(page), "no host name or address");
  assert.ok(!/(secret|token|password|api[_-]?key|bearer)/i.test(page.replace(/<style[\s\S]*?<\/style>/, "")), "no secret-shaped word");
  const contact = page.match(/send everything to Colton/g) ?? [];
  assert.equal(contact.length, 1, "the contact line appears once");
  assert.ok(!/Colton/.test(page.replace("send everything to Colton", "")), "the only person's name is the contact line");
});
