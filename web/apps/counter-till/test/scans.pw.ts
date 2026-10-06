// The two source scans, held as a test so the CI keeps them green and not
// just this run's report:
//
//   1. No money arithmetic in the app: the plan's scan over the app's source
//      prints nothing outside the named display and parse helpers. The app
//      formats what the core computed, through the named helpers, and
//      computes nothing. The scan reads both spellings of a cents figure, the
//      snake_case the wire carries and the camelCase the ported surface's
//      classes carry, beside any arithmetic operator (the review's P1-1: a
//      `savingCents * qty` must not hide from the snake_case spelling), and
//      every binary operator is read, plus included: the reference's own
//      `s + l.lineTotalCents` subtotal reducer is money arithmetic in the
//      browser, exactly what this scan exists to catch. A unary minus passed
//      to a formatter is display shaping, not a figure decided, so the minus
//      wants the binary left operand.
//   2. No card number path: the till has no card number field and loads no
//      card tokenization script; the card tender sends the fixed demo token.
//
// This spec reads the same source tree the greps walk, with the same intent:
// a hit anywhere outside the named helpers for the first scan, or anywhere at
// all for the second, is a failure naming the file.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const SRC = fileURLToPath(new URL("../src", import.meta.url));
const MONEY_HELPER = "money.ts";
// The named display and parse helper of the ported surface, the one place
// its integer cents become a readable amount and the one place its division
// spelling lives. Its internals divide by one hundred to format; that is the
// formatting, not a figure decided, so the arithmetic scan walks everything
// else and this file is named here, alone. The app's own money helper stays
// IN the scanned set: it formats integers and parses one typed string through
// the decimal string and computes nothing, so it owes the scan zero hits, and
// if it ever hits, that is a finding, not an exclusion.
// Beside it the seam file, ports.ts: the port rerouted the reference's
// client-side money computations there (CLOSURE.md lists each; of the
// reference's thirteen assemblies, the ticket tax and the ticket total were
// removed outright by row 661 and the seam carries neither), and
// the wiring item answers the rerouted ones in that one file as display
// assembly over figures the core stated, each pinned by ports-money.test.mjs.
// The ported closure and the app's own views keep owing the scan zero hits:
// the exclusion names the boundary where the reroute lands, not a licence,
// and a third file that divides figures by hand must still face the scan.
// HH note: the display helper and the seam file moved into
// @hardscapehouse/pos-kit (web/packages/pos-kit, src/ported, same names)
// with the shared pieces, so the two names point across at the kit's
// copies: they sit OUTSIDE the walked tree and suppress nothing here, they
// only keep the boundary where the reroute lands named beside the claim.
// The kit's scan copy (the kit's scans.test.mjs) makes the same claim over
// the kit's tree and carries the working exclusions; the app's own walk
// below keeps owing the scan zero hits: no app tree file may join the list.
const NAMED_HELPERS = [
  join(SRC, "..", "..", "..", "packages", "pos-kit", "src", "ported", "_money-format.ts"),
  join(SRC, "..", "..", "..", "packages", "pos-kit", "src", "ported", "ports.ts"),
];

function walkSources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walkSources(full, out);
    else if (entry.endsWith(".ts")) out.push(full);
  }
  return out;
}

test("the money scan prints nothing outside the named display and parse helpers", () => {
  // Four shapes, all of them a cents-named operand beside arithmetic:
  //   the operand before the operator (`savingCents * qty`),
  //   times and divided after a binary operand (`q * unit_price_cents`),
  //   plus after a binary operand (`s + l.lineTotalCents`, the reducer shape),
  //   and a binary minus after a binary operand (`total - taxCents`), where
  //   the identifier may not be the bare kebab suffix of an attribute name
  //   and a key shape (`- cents:`) is not arithmetic. A unary minus passed to
  //   a formatter (`fmtCents(-state.remainingCents)`) is display shaping of a
  //   figure the seam answered, not a figure decided, so the minus wants a
  //   binary left operand.
  const scan =
    /[a-zA-Z_$.]*[cC]ents\s*[-+*/]|[a-zA-Z0-9_$)\]]\s*[+*/]\s*[a-zA-Z_$.]*[cC]ents\b|[a-zA-Z0-9_$)\]]\s*-\s*(?![cC]ents\b)[a-zA-Z_$.]*[cC]ents\b\s*[^:]/;
  // String literals hold words, not computations, so quoted spans come off
  // the line before the scan (an attribute named 'total-cents' is a name).

  const offenders: string[] = [];
  const scanned: string[] = [];
  for (const file of walkSources(SRC)) {
    if (NAMED_HELPERS.includes(file)) continue;
    scanned.push(file);
    const hits = readFileSync(file, "utf8")
      .split("\n")
      .map((line) => line.replace(/'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"/g, '""'))
      .filter((line) => scan.test(line));
    if (hits.length === 0) continue;
    offenders.push(`${file}: ${hits.join(" | ")}`);
  }
  // The exclusion is exactly the two named files and nothing else: the one
  // display helper, and the one seam file that answers the rerouted
  // computations. A third file that divides figures by hand must face the
  // scan, not join the list, and the app's own money helper stays in the
  // walked set and owes zero hits.
  expect(NAMED_HELPERS).toHaveLength(2);
  expect(NAMED_HELPERS.some((file) => file.endsWith("_money-format.ts"))).toBe(true);
  expect(NAMED_HELPERS.some((file) => file.endsWith("ports.ts"))).toBe(true);
  // The walk covers the app's own views, the app's money helper and the
  // ported tree: the claim is about the tree, so the walk must be seen to
  // reach all three.
  expect(scanned.some((file) => file.endsWith(MONEY_HELPER))).toBe(true);
  expect(scanned.length).toBeGreaterThan(20);
  expect(offenders).toEqual([]);
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
