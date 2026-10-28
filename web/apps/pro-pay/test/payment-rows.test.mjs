// PR 668 fix round 2 (the money wave 2 read's P3-2): a returned payment's row on Statements.
// The payment history now carries the application row and the reversal beside it, so the row
// lists each invoice once, from the application rows (positive amounts), and a returned
// payment (bounced or voided) reads as returned in the status chip's own words, never
// "Applied to".
//
// Red first: today the bounced row reads "Applied to INV-20401, INV-20402, INV-20401,
// INV-20402".
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/payment-rows.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { statementsScreen } from "../src/views.ts";

const INVOICES = [
  { id: "inv-20401", invoice_no: "INV-20401" },
  { id: "inv-20402", invoice_no: "INV-20402" },
  { id: "inv-20403", invoice_no: "INV-20403" },
];

/** The markup of a Lit template, as text: the static chunks with the nested templates folded in. */
function markup(node) {
  if (node === null || node === undefined || node === false || node === true) return "";
  if (typeof node === "symbol") return "";
  if (Array.isArray(node)) return node.map(markup).join("");
  if (typeof node === "object" && Array.isArray(node.strings)) {
    let out = "";
    node.strings.forEach((chunk, i) => {
      out += chunk;
      if (i < node.values.length) out += markup(node.values[i]);
    });
    return out;
  }
  return String(node);
}

function stateWith(payments) {
  return {
    today: "2026-03-10",
    canPay: false,
    invoices: INVOICES,
    summary: null,
    selected: new Set(),
    openInvoice: null,
    sheet: null,
    waiting: false,
    methods: [],
    methodAdd: { open: false, makeDefault: false, busy: false, words: "", nonce: null, frameAvailable: false },
    confirmRemove: null,
    methodWords: "",
    payments,
  };
}

function pay(over) {
  return {
    id: "p-1",
    payment_no: "PMT-9001",
    customer_id: "cust-1",
    method: "CHEQUE",
    amount_cents: 80_000,
    status: "RECEIVED",
    gateway_ref: "",
    received_at: "2026-03-03T13:00:00Z",
    allocations: [],
    ...over,
  };
}

const times = (text, word) => text.split(word).length - 1;

test("a bounced payment's row says Bounced, names each invoice once from the application rows, never Applied to", () => {
  const bounced = pay({
    payment_no: "PMT-9101",
    status: "BOUNCED",
    allocations: [
      { invoice_id: "inv-20401", amount_cents: 50_000, created_at: "2026-03-03T13:00:00Z" },
      { invoice_id: "inv-20402", amount_cents: 30_000, created_at: "2026-03-03T13:00:00Z" },
      { invoice_id: "inv-20401", amount_cents: -50_000, created_at: "2026-03-04T09:00:00Z" },
      { invoice_id: "inv-20402", amount_cents: -30_000, created_at: "2026-03-04T09:00:00Z" },
    ],
  });
  const text = markup(statementsScreen(stateWith([bounced])));
  assert.ok(text.includes("Bounced · INV-20401, INV-20402"), text);
  assert.ok(!text.includes("Applied to"), text);
  assert.equal(times(text, "INV-20401"), 1, text);
  assert.equal(times(text, "INV-20402"), 1, text);
});

test("a voided payment's row says Void, names the invoice once, never Applied to", () => {
  const voided = pay({
    payment_no: "PMT-9102",
    status: "VOID",
    amount_cents: 25_000,
    allocations: [
      { invoice_id: "inv-20403", amount_cents: 25_000, created_at: "2026-03-03T13:00:00Z" },
      { invoice_id: "inv-20403", amount_cents: -25_000, created_at: "2026-03-03T14:00:00Z" },
    ],
  });
  const text = markup(statementsScreen(stateWith([voided])));
  assert.ok(text.includes("Void · INV-20403"), text);
  assert.ok(!text.includes("Applied to"), text);
  assert.equal(times(text, "INV-20403"), 1, text);
});

test("a normal received row is unchanged: Applied to, each invoice once", () => {
  const received = pay({
    allocations: [
      { invoice_id: "inv-20401", amount_cents: 50_000, created_at: "2026-03-03T13:00:00Z" },
      { invoice_id: "inv-20402", amount_cents: 30_000, created_at: "2026-03-03T13:00:00Z" },
    ],
  });
  const text = markup(statementsScreen(stateWith([received])));
  assert.ok(text.includes("Applied to INV-20401, INV-20402"), text);
});
