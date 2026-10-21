// UX-97: a caller the dealer's account position policy refuses gets a workspace with the credit
// limit, open, available, overdue, utilization and aging absent. The screen says so in words and
// never shows "$0.00" or an empty bar for a figure it was not given.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ACCOUNT_POSITION_WITHHELD_WORDS,
  accountPositionOf,
  renderWorkspaceHeader,
  renderWorkspaceMobileSummary,
  renderWorkspaceOverview,
  renderWorkspaceSummaryCard,
} from "../src/ar/ar-workspace.ts";

function flat(v) {
  if (v == null || v === false) return "";
  if (Array.isArray(v)) return v.map(flat).join("");
  if (typeof v === "object" && v.strings) {
    return Array.from(v.strings).map((s, i) => s + (i < v.values.length ? flat(v.values[i]) : "")).join("");
  }
  return typeof v === "symbol" ? "" : String(v);
}

const aging = { current_cents: 90000, days_30_cents: 50000, days_60_cents: 0, days_90_cents: 0, days_90_plus_cents: 0, open_cents: 140000, overdue_cents: 50000 };
const identity = { customer_id: "c1", name: "Northgate", account_no: "NG-1", status: "CREDIT_HOLD", terms_days: 30 };
const allowed = {
  header: {
    ...identity, account_position_withheld: false,
    credit_limit_cents: 500000, open_cents: 140000, available_credit_cents: 360000, overdue_cents: 50000, utilization_bps: 2800, aging,
  },
  invoices_open: [], invoices_closed: [], invoices_disputed: [], credit_notes: [], statements: [], notes: [], activity: [],
  payments_withheld: false, as_of: "2026-08-11T00:00:00Z",
};
const refused = { ...allowed, header: { ...identity, account_position_withheld: true } };

test("an admitted caller reads the whole position", () => {
  assert.equal(accountPositionOf(allowed.header)?.credit_limit_cents, 500000);
  const out = [
    renderWorkspaceHeader(allowed, "overview", () => {}),
    renderWorkspaceOverview(allowed),
    renderWorkspaceSummaryCard(allowed, () => {}),
    renderWorkspaceMobileSummary(allowed, () => {}),
  ].map(flat).join("\n");
  for (const want of ["5,000.00", "1,400.00", "3,600.00", "500.00", "Credit limit", "28% used"]) {
    assert.ok(out.includes(want), `${want} missing`);
  }
  assert.ok(!out.includes(ACCOUNT_POSITION_WITHHELD_WORDS));
});

test("a refused caller reads words, never a zero or an empty bar", () => {
  assert.equal(accountPositionOf(refused.header), undefined);
  const out = [
    renderWorkspaceHeader(refused, "overview", () => {}),
    renderWorkspaceOverview(refused),
    renderWorkspaceSummaryCard(refused, () => {}),
    renderWorkspaceMobileSummary(refused, () => {}),
  ].map(flat).join("\n");
  assert.ok(out.includes(ACCOUNT_POSITION_WITHHELD_WORDS));
  for (const bad of ["$0.00", "0.00", "0%", "Credit limit", "credit-bar", "bucket-current", "Bucket breakdown", "past due", "Past due"]) {
    assert.ok(!out.includes(bad), `${bad} must not render for a refused caller: ${out}`);
  }
  // The hold standing is operational and stays.
  assert.ok(out.includes("Credit hold") || out.includes("status-credit_hold"));
});

test("a half set of figures is withheld, not rendered as if whole", () => {
  const half = { ...allowed.header };
  delete half.available_credit_cents;
  assert.equal(accountPositionOf(half), undefined);
});
