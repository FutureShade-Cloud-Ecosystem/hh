// HH-681 Manager Home change orders: pure helpers for status words, op words,
// money formatting, allowed actions, and idempotency-key reuse.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  changeOrderAllowsReject,
  changeOrderAllowsSignApply,
  changeOrderOpWord,
  changeOrderStatusWord,
  formatChangeOrderCents,
  keyForSignApply,
  nextIdempotencyKey,
} from "../src/approvals-logic.ts";

test("change order status words map the server statuses to plain language", () => {
  assert.equal(changeOrderStatusWord("PENDING_FINANCE"), "Waiting on finance");
  assert.equal(changeOrderStatusWord("APPROVED"), "Approved, ready to apply");
  assert.equal(changeOrderStatusWord("PENDING_YARD"), "PENDING_YARD");
});

test("change order op words map the line ops to plain language", () => {
  assert.equal(changeOrderOpWord("ADD"), "Add");
  assert.equal(changeOrderOpWord("QTY"), "Change quantity");
  assert.equal(changeOrderOpWord("REMOVE"), "Remove");
  assert.equal(changeOrderOpWord("UNKNOWN"), "UNKNOWN");
});

test("change order cents formatting uses the shared money formatter", () => {
  assert.equal(formatChangeOrderCents(123456), "$1,234.56");
  assert.equal(formatChangeOrderCents(-123456), "-$1,234.56");
  assert.equal(formatChangeOrderCents(0), "$0.00");
});

test("allowed actions fail closed on an absent list", () => {
  assert.equal(changeOrderAllowsSignApply(undefined), false);
  assert.equal(changeOrderAllowsSignApply(null), false);
  assert.equal(changeOrderAllowsSignApply([]), false);
  assert.equal(changeOrderAllowsReject(undefined), false);
  assert.equal(changeOrderAllowsReject(null), false);
  assert.equal(changeOrderAllowsReject([]), false);
});

test("allowed actions read the server's next_actions list", () => {
  assert.equal(changeOrderAllowsSignApply(["sign_apply"]), true);
  assert.equal(changeOrderAllowsSignApply(["reject"]), false);
  assert.equal(changeOrderAllowsReject(["reject"]), true);
  assert.equal(changeOrderAllowsReject(["sign_apply"]), false);
});

test("idempotency keys are UUIDs and each press gets its own key", () => {
  const a = nextIdempotencyKey();
  const b = nextIdempotencyKey();
  assert.match(a, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);
  assert.notEqual(a, b);
});

test("a retry reuses the pending idempotency key; a new press generates a new one", () => {
  const first = keyForSignApply(null, "c1");
  assert.equal(first.reused, false);
  assert.equal(first.pending.id, "c1");
  assert.match(first.pending.key, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);

  const retry = keyForSignApply(first.pending, "c1");
  assert.equal(retry.reused, true);
  assert.equal(retry.pending.key, first.pending.key);

  const other = keyForSignApply(first.pending, "c2");
  assert.equal(other.reused, false);
  assert.notEqual(other.pending.key, first.pending.key);
});
