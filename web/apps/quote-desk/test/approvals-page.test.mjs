// The approvals reader takes the contract's page object and the core's bare
// array (the two disagree today), and refuses a page with one unreadable row.
import assert from "node:assert/strict";
import { test } from "node:test";
import { readApprovalsPage } from "../src/_seam-wire.ts";

const row = { id: "a1", requested_by: "u1", branch_id: "b1", policy_type: "MARGIN_BELOW_MINIMUM", detail: "Quote Q-1", status: "PENDING" };

test("a bare array reads as one page with no cursor", () => {
  const page = readApprovalsPage([row]);
  assert.equal(page?.items.length, 1);
  assert.equal(page?.nextCursor, undefined);
});

test("a page object reads its items and follows next_cursor", () => {
  const page = readApprovalsPage({ items: [row], next_cursor: "c2" });
  assert.equal(page?.items[0]?.id, "a1");
  assert.equal(page?.nextCursor, "c2");
  assert.equal(readApprovalsPage({ items: [row], next_cursor: "" })?.nextCursor, undefined);
});

test("one unreadable row, a bad cursor or another shape refuses the page", () => {
  assert.equal(readApprovalsPage([row, { ...row, status: "MAYBE" }]), null);
  assert.equal(readApprovalsPage([row, { ...row, id: 7 }]), null);
  assert.equal(readApprovalsPage([{ ...row, policy_type: "OTHER" }]), null);
  assert.equal(readApprovalsPage({ items: [row], next_cursor: 5 }), null);
  assert.equal(readApprovalsPage({ rows: [row] }), null);
  assert.equal(readApprovalsPage("x"), null);
});
