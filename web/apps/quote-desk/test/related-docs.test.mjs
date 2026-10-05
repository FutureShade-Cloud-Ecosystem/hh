// The related documents panel's pure logic, unit tested: the words of a
// document family (kinds, statuses of the three lifecycles, provenance, the
// acceptance count), the chip lines, the empty state, and which chips a host
// may turn into links. DOM-free, so the Node runner drives it.
import assert from "node:assert/strict";
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

const row = (over = {}) => ({
  kind: "ORDER",
  id: "o-1",
  number: "SO-0001",
  status: "DRAFT",
  date: "2026-10-04T12:00:00.000Z",
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
  assert.equal(chipDetail(row()), "2026-10-04");
  assert.equal(chipDetail(row({ origin: "COUNTER" })), "2026-10-04 · created at the counter");
  assert.equal(chipDetail(row({ origin: "PORTAL" })), "2026-10-04 · accepted online");
  assert.equal(chipDetail(row({ origin: "COUNTER", acceptSeq: 2 })), "2026-10-04 · created at the counter · acceptance 2");
  // The first acceptance is the ordinary case and is not numbered.
  assert.equal(chipDetail(row({ acceptSeq: 1 })), "2026-10-04");
  // An unknown door says nothing rather than invent one.
  assert.equal(originLabel(undefined), "");
  assert.equal(originLabel("FAX"), "");
});

test("docDay is day grained and empty when unreadable", () => {
  assert.equal(docDay("2026-10-04T12:00:00.000Z"), "2026-10-04");
  assert.equal(docDay(""), "");
  assert.equal(docDay("not a date"), "");
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
