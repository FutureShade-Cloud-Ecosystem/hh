// The related documents panel's pure logic, unit tested: the words of a
// document family (kinds, statuses of the three lifecycles, provenance, the
// acceptance count), the chip lines, the empty state, and which chips a host
// may turn into links. DOM-free, so the Node runner drives it.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import {
  chipDetail,
  chipText,
  docDay,
  docStatusWord,
  familyEmptyText,
  familyIsOnlySelf,
  isOpenable,
  kindLabel,
  originLabel,
} from "../src/_related-docs.js";

// The fixture's instant, built from numbers so no committed line reads as a calendar date,
// and chosen at noon UTC so its day differs between the two ends of the zone range a dealer's
// browser can sit in (the zone test below pins exactly that). The day the chip shows is the
// LOCAL day, so every expectation here is derived from the instant itself, never typed as a
// date: this file asserts the same truths in whatever zone runs it.
const DAY = new Date(Date.UTC(2026, 9, 4, 12));
const DAY_ISO = DAY.toISOString();
const DAY_WORD = DAY.toLocaleDateString("en-CA");
/** The local day of the fixture instant at a whole minute offset from UTC, as docDay words it. */
const dayAtOffset = (minutes) => new Date(DAY.getTime() + minutes * 60000).toISOString().slice(0, 10);

const row = (over = {}) => ({
  kind: "ORDER",
  id: "o-1",
  number: "SO-0001",
  status: "DRAFT",
  date: DAY_ISO,
  totalCents: 4068,
  ...over,
});

test("kind labels the three kinds and degrades on an unknown kind", () => {
  assert.equal(kindLabel("QUOTE"), "Quote");
  assert.equal(kindLabel("ORDER"), "Order");
  assert.equal(kindLabel("INVOICE"), "Invoice");
  assert.equal(kindLabel("CREDIT_NOTE"), "CREDIT_NOTE");
});

test("each lifecycle keeps its own status words", () => {
  assert.equal(docStatusWord("QUOTE", "DRAFT"), "Draft");
  assert.equal(docStatusWord("QUOTE", "CONVERTED"), "Converted");
  assert.equal(docStatusWord("ORDER", "PARTIALLY_FULFILLED"), "Partly fulfilled");
  assert.equal(docStatusWord("ORDER", "INVOICED"), "Invoiced");
  assert.equal(docStatusWord("INVOICE", "FINALIZED"), "Open");
  assert.equal(docStatusWord("INVOICE", "PARTIALLY_PAID"), "Part paid");
  assert.equal(docStatusWord("INVOICE", "PAID"), "Paid");
  // An unmapped status comes back as itself, lowercased, never as an invented phrase.
  assert.equal(docStatusWord("ORDER", "QUARANTINED"), "quarantined");
  // An unknown kind is not guessed at as an invoice.
  assert.equal(docStatusWord("CREDIT_NOTE", "OPEN"), "open");
});

test("chipText is the kind, the number and the status", () => {
  assert.equal(chipText(row()), "Order SO-0001 · Draft");
  assert.equal(chipText(row({ kind: "INVOICE", number: "INV-3001", status: "PAID" })), "Invoice INV-3001 · Paid");
});

test("chipDetail carries the day, the provenance door and a repeat acceptance", () => {
  assert.equal(chipDetail(row()), DAY_WORD);
  assert.equal(chipDetail(row({ origin: "COUNTER" })), `${DAY_WORD} · created at the counter`);
  assert.equal(chipDetail(row({ origin: "PORTAL" })), `${DAY_WORD} · accepted online`);
  assert.equal(chipDetail(row({ origin: "COUNTER", acceptSeq: 2 })), `${DAY_WORD} · created at the counter · acceptance 2`);
  // The first acceptance is the ordinary case and is not numbered.
  assert.equal(chipDetail(row({ acceptSeq: 1 })), DAY_WORD);
  // An unknown door says nothing rather than invent one.
  assert.equal(originLabel(undefined), "");
  assert.equal(originLabel("FAX"), "");
});

test("docDay is day grained and empty when unreadable", () => {
  assert.equal(docDay(DAY_ISO), DAY_WORD);
  assert.equal(docDay(""), "");
  assert.equal(docDay("not a date"), "");
});

test("docDay is the local day wherever the desk runs, pinned at both ends of the zone range", () => {
  // The chip's day is the LOCAL day of the document, so a desk at UTC+14 and one at UTC-12 can
  // read a different day off the same instant, both right. Each zone is spawned as its own
  // process with TZ set (the program is fed on stdin, so no file is written), because the
  // runner's own zone is whatever launched it; the expected day is derived from the instant's
  // epoch at each offset, never typed as a date. The .ts source is named in a static import
  // because the loader's hooks rewrite only relative specifiers, and Node's own type stripping
  // loads the .ts source directly.
  const loader = new URL("../../../packages/api-client/src/_ts-js-loader.mjs", import.meta.url).href;
  const moduleUrl = new URL("../src/_related-docs.ts", import.meta.url).href;
  const script = 'import { docDay } from "' + moduleUrl + '";\nprocess.stdout.write(docDay(' + JSON.stringify(DAY_ISO) + '));';
  // The probe is a NODE question (the browser and the desk both run on it), so when this file
  // runs under another runner whose execPath is not node (bun test), node is taken from the PATH.
  const node = process.execPath.includes("node") ? process.execPath : "node";
  for (const [zone, offsetMinutes] of [
    ["Pacific/Kiritimati", 14 * 60],
    ["Etc/GMT+12", -12 * 60],
  ]) {
    const run = spawnSync(node, ["--import", loader, "--input-type=module"], {
      input: script,
      encoding: "utf8",
      env: { ...process.env, TZ: zone },
    });
    assert.equal(run.status, 0, `${zone}: ${run.stderr}`);
    assert.equal(run.stdout.trim(), dayAtOffset(offsetMinutes), zone);
  }
});

test("a chip is a link only in a host that can open its kind, and never the self row", () => {
  assert.equal(isOpenable(row(), "QUOTE ORDER"), true);
  assert.equal(isOpenable(row(), "QUOTE"), false);
  assert.equal(isOpenable(row({ self: true }), "ORDER"), false);
  assert.equal(isOpenable(row(), ""), false);
  assert.equal(isOpenable(row(), "  ORDER  INVOICE "), true);
});

test("the empty state says what would make the list grow", () => {
  assert.equal(familyEmptyText("QUOTE"), "No related documents yet: converting this quote to an order links them here.");
  assert.equal(familyEmptyText("ORDER"), "No related documents yet: nothing else is linked to this order.");
  assert.equal(familyIsOnlySelf([]), true);
  assert.equal(familyIsOnlySelf([row({ self: true })]), true);
  assert.equal(familyIsOnlySelf([row({ self: true }), row({ kind: "INVOICE" })]), false);
});
