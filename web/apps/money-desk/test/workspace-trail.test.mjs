// UX-93: a caller without the payment trail gets a workspace with the payment figures absent. The
// screen says so in words and never shows "$0.00" or "No payments" for a figure it was not given.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  WITHHELD_WORDS,
  lastPaymentFacts,
  renderWorkspaceHeader,
  renderWorkspaceOverview,
  renderWorkspacePayments,
  renderWorkspaceSummaryCard,
} from "../src/ar/ar-workspace.ts";

// flat reads a lit template, nested templates and arrays included, as the text it would carry.
function flat(v) {
  if (v == null || v === false) return "";
  if (Array.isArray(v)) return v.map(flat).join("");
  if (typeof v === "object" && Array.isArray(v.strings)) {
    return v.strings.map((s, i) => s + (i < v.values.length ? flat(v.values[i]) : "")).join("");
  }
  if (typeof v === "object" && v.strings) {
    return Array.from(v.strings).map((s, i) => s + (i < v.values.length ? flat(v.values[i]) : "")).join("");
  }
  return typeof v === "symbol" ? "" : String(v);
}

const aging = { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_90_cents: 0, days_90_plus_cents: 0, open_cents: 0, overdue_cents: 0 };
const header = {
  customer_id: "c1", name: "Northgate", account_no: "NG-1", status: "CURRENT", terms_days: 30,
  credit_limit_cents: 500000, open_cents: 0, available_credit_cents: 500000, overdue_cents: 0, utilization_bps: 0, aging,
};
const base = {
  header, invoices_open: [], invoices_closed: [], invoices_disputed: [], credit_notes: [], statements: [], notes: [],
  activity: [], as_of: "2026-08-11T00:00:00Z",
};
const withheld = { ...base, payments_withheld: true };
const none = { ...base, payments: [], payments_withheld: false };
const held = {
  ...base, payments_withheld: false,
  header: { ...header, last_payment_at: "2026-08-10T10:00:00Z", last_payment_cents: 41234 },
  payments: [{ id: "p1", payment_no: "PAY-1", received_at: "2026-08-10T10:00:00Z", amount_cents: 41234, method: "CHEQUE", unapplied_cents: 0 }],
};

test("an absent payment figure is withheld, not none", () => {
  assert.deepEqual(lastPaymentFacts(withheld), { kind: "withheld" });
  assert.deepEqual(lastPaymentFacts(none), { kind: "none" });
  assert.equal(lastPaymentFacts(held).kind, "paid");
});

test("a withheld workspace says so and shows no payment figure", () => {
  const out = [
    renderWorkspaceHeader(withheld, "overview", () => {}),
    renderWorkspaceOverview(withheld),
    renderWorkspacePayments(withheld, () => {}),
    renderWorkspaceSummaryCard(withheld, () => {}),
  ].map(flat).join("\n");
  assert.ok(out.includes(WITHHELD_WORDS));
  assert.ok(!out.includes("No payments"), "absence must not read as no payments");
  assert.ok(!out.includes("no payments on file"));
  assert.ok(!out.includes("Payments (0)"));
  assert.ok(!out.includes("<table class=\"payment-table\""));
});

test("a caller with the trail and no payments still reads no payments", () => {
  assert.ok(flat(renderWorkspacePayments(none, () => {})).includes("No payments on this account."));
});

test("a caller with the trail sees the full row", () => {
  const out = flat(renderWorkspacePayments(held, () => {}));
  assert.ok(out.includes("PAY-1") && out.includes("CHEQUE") && out.includes("412.34"));
  assert.ok(!out.includes(WITHHELD_WORDS));
});
