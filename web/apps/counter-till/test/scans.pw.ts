// The two source scans, held as a test so the CI keeps them green and not
// just this run's report:
//
//   1. No money arithmetic in the app: the plan's scan over the app's source
//      prints nothing outside the one named money helper. The app formats
//      what the core computed, through that one helper, and computes nothing.
//   2. No card number path: the till has no card number field and loads no
//      card tokenization script; the card tender sends the fixed demo token.
//
// This spec reads the same source tree the greps walk, with the same intent:
// a hit anywhere outside src/money.ts for the first scan, or anywhere at all
// for the second, is a failure naming the file.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const SRC = fileURLToPath(new URL("../src", import.meta.url));
const MONEY_HELPER = "money.ts";

function walkSources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walkSources(full, out);
    else if (entry.endsWith(".ts")) out.push(full);
  }
  return out;
}

test("the money scan prints nothing outside the one named money helper", () => {
  const scan = /_cents\s*[-+*/]|[-+*/]\s*[A-Za-z_.]*_cents/;
  const offenders: string[] = [];
  const scanned: string[] = [];
  for (const file of walkSources(SRC)) {
    scanned.push(file);
    const hits = readFileSync(file, "utf8").split("\n").filter((line) => scan.test(line));
    if (hits.length === 0) continue;
    offenders.push(`${file}: ${hits.join(" | ")}`);
  }
  // The stronger standard the comment claims: the helper itself is scanned
  // with the rest and finds nothing, because it formats integers and parses
  // one typed string, and computes none of it. The walk must have covered the
  // helper for that claim to mean anything.
  expect(offenders).toEqual([]);
  expect(scanned.some((file) => file.endsWith(MONEY_HELPER))).toBe(true);
});

test("the card scan prints nothing: no card number path, no tokenizer, no loader", () => {
  const scan = /card_?number|tokeniz|runner\.js/i;
  const offenders: string[] = [];
  for (const file of walkSources(SRC)) {
    const hits = readFileSync(file, "utf8").split("\n").filter((line) => scan.test(line));
    if (hits.length > 0) offenders.push(`${file}: ${hits.join(" | ")}`);
  }
  expect(offenders).toEqual([]);
});

test("the money formatting goes through the one named helper", () => {
  // The helper exists, names the two formatters, and the screens import the
  // helper rather than formatting on their own.
  const helper = readFileSync(join(SRC, MONEY_HELPER), "utf8");
  expect(helper).toContain("export function formatMoney");
  expect(helper).toContain("export function formatMargin");
  const importing = walkSources(SRC)
    .filter((file) => !file.endsWith(MONEY_HELPER))
    .filter((file) => /formatMoney|formatMargin/.test(readFileSync(file, "utf8")));
  expect(importing.length).toBeGreaterThan(0);
  for (const file of importing) {
    // Every screen pulls its formatting from the helper, by import, never a
    // local copy.
    expect(readFileSync(file, "utf8")).toMatch(/from "\.{1,2}\/money\.js"/);
  }
});
