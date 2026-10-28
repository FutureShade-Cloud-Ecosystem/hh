// PR 668 fix round 3: the portal row drops the inline status word. The chip carries the
// state and the line carries the facts, so a returned payment (bounced or voided) names
// each invoice once, from the application rows (positive amounts), with no status word on
// the line. A returned payment with no application row ends the line after the moment:
// "no invoice yet" is the received row's empty list, the object of "Applied to", and the
// returned line has no list and no verb to carry it.
//
// Red first: today the bounced row reads "Cheque · Mar 3, 2026, 8:00 AM · Bounced ·
// INV-20401, INV-20402".
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

test("a bounced payment's line names each invoice once from the application rows, with no status word", () => {
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
  assert.ok(text.includes("8:00 AM · INV-20401, INV-20402"), text);
  assert.ok(!text.includes("Bounced ·"), text);
  assert.ok(!text.includes("Applied to"), text);
  assert.equal(times(text, "INV-20401"), 1, text);
  assert.equal(times(text, "INV-20402"), 1, text);
});

test("a voided payment's line names the invoice once, with no status word", () => {
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
  assert.ok(text.includes("8:00 AM · INV-20403"), text);
  assert.ok(!text.includes("Void ·"), text);
  assert.ok(!text.includes("Applied to"), text);
  assert.equal(times(text, "INV-20403"), 1, text);
});

test("a returned payment with no invoice ends the line after the moment", () => {
  const returned = pay({ payment_no: "PMT-9103", status: "VOID", allocations: [] });
  const text = markup(statementsScreen(stateWith([returned])));
  assert.ok(text.includes("Cheque · Mar 3, 2026, 8:00 AM"), text);
  assert.ok(!text.includes("8:00 AM ·"), text);
  assert.ok(!text.includes("no invoice yet"), text);
  assert.ok(!text.includes("Applied to"), text);
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

test("a received payment with no invoice keeps the existing no invoice yet words", () => {
  const received = pay({ allocations: [] });
  const text = markup(statementsScreen(stateWith([received])));
  assert.ok(text.includes("Applied to no invoice yet"), text);
});
