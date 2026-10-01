// The testers' page held against the claims ledger: every behavior sentence on
// the served page carries a claim id, and the id set on the page must equal
// the ledger's covered set, in both directions. The page is served by the
// image's app (the built testers page), behind the same phrase gate as the
// till, so this spec fetches it the way a tester's browser does.
import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { CLAIM_COVERAGE } from "./claims.js";
import { shot } from "./helpers.js";

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
  // Each entry names the specs that carry the proof; those files must exist,
  // so the ledger cannot outlive the run it names.
  for (const proof of Object.values(CLAIM_COVERAGE)) {
    for (const name of proof.matchAll(/\d\d-[a-z]+(?=\.pw| )/g)) {
      const spec = join(fileURLToPath(new URL(".", import.meta.url)), `${name[0]}.pw.ts`);
      expect(
        (() => {
          try {
            readFileSync(spec);
            return true;
          } catch {
            return false;
          }
        })(),
        `the ledger names ${spec}, which does not exist`,
      ).toBe(true);
    }
  }
  await shot(page, "40-claims-testers-page", testInfo);
});
