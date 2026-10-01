// The testers' page held against the claims ledger: every behavior sentence on
// the served page carries a claim id, and three sets must agree. The page's id
// set and the ledger's key set are held equal in both directions, so the page
// can never quietly claim what the run does not prove and a retired proof
// cannot outlive its sentence. Each ledger proof names a spec file and the
// EXACT test title inside it, and every named title is asserted to exist in
// that file, so a proof cannot drift away from the assertion it names. The
// page is served by the image's app (the built testers page), behind the same
// phrase gate as the till, so this spec fetches it the way a tester's browser
// does.
import { expect, test } from "@playwright/test";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { CLAIM_COVERAGE } from "./claims.js";
import { shot } from "./helpers.js";

/** Every test title the named spec file declares, read off its source. */
function titlesIn(dir: string, file: string): Set<string> {
  const source = readFileSync(join(dir, file), "utf8");
  const found = new Set<string>();
  for (const match of source.matchAll(/\btest\(\s*"((?:[^"\\]|\\.)*)"/g)) {
    found.add(match[1]);
  }
  return found;
}

test("every claim on the served testers' page is covered by the run, and every covered claim is on the page", async ({
  page,
}, testInfo) => {
  const answer = await page.goto("/testers.html");
  expect(answer?.status()).toBe(200);
  const served = await page.content();
  const claims = new Set(
    [...served.matchAll(/data-claim="([a-z0-9-]+)"/g)].map((match) => match[1]),
  );
  const covered = new Set(Object.keys(CLAIM_COVERAGE));
  const uncovered = [...claims].filter((claim) => !covered.has(claim));
  expect(uncovered, "the page claims what the run does not prove").toEqual([]);
  const stale = [...covered].filter((claim) => !claims.has(claim));
  expect(stale, "the ledger covers a sentence the page no longer makes").toEqual([]);

  // Each proof names a spec file and the exact test title in it: the title
  // must exist, or the ledger names an assertion that is not there.
  const dir = fileURLToPath(new URL(".", import.meta.url));
  const titleCache = new Map<string, Set<string>>();
  const titlesFor = (file: string): Set<string> => {
    let titles = titleCache.get(file);
    if (titles === undefined) {
      titles = titlesIn(dir, file);
      titleCache.set(file, titles);
    }
    return titles;
  };
  for (const [claim, proofs] of Object.entries(CLAIM_COVERAGE)) {
    expect(proofs.length, `the claim ${claim} names at least one proof`).toBeGreaterThan(0);
    for (const proof of proofs) {
      expect(
        readdirSync(dir).includes(proof.file),
        `the claim ${claim} names ${proof.file}, which does not exist`,
      ).toBe(true);
      expect(
        titlesFor(proof.file).has(proof.test),
        `the claim ${claim} names the test "${proof.test}" in ${proof.file}, where no such title exists`,
      ).toBe(true);
    }
  }
  await shot(page, "40-claims-testers-page", testInfo);
});
