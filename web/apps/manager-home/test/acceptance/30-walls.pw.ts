// The walls leg: the surfaces the matrix refuses this role answer with the
// gate's own refusal (the till and the Quote Desk are None for the yard
// manager, row 1156), and cost is visible where the matrix says it is (the
// yard manager sits on the cost_visibility list, so the receiving dock's
// still-open value arrives on the row).
import { test, expect } from "@playwright/test";
import { mint, staffGet } from "./helpers.js";

const TRENTON = "5d1b3f6e-0000-4000-9000-000000000001";

test("the counter till is refused for the yard manager", async ({ page }) => {
  const bearer = await mint(page, "yard-manager");
  const parked = await staffGet(page, bearer, `/api/v1/pos/parked?branch_id=${TRENTON}`);
  expect(parked.status).toBe(403);
});

test("the Quote Desk is refused for the yard manager", async ({ page }) => {
  const bearer = await mint(page, "yard-manager");
  const quotes = await staffGet(page, bearer, "/api/v1/quotes?limit=1");
  expect(quotes.status).toBe(403);
});

test("the approvals the yard manager does not decide are refused in the core's words", async ({ page }) => {
  const bearer = await mint(page, "yard-manager");
  const margin = await staffGet(page, bearer, "/api/v1/staff/approvals?kind=MARGIN_BELOW_MINIMUM");
  expect(margin.status).toBe(403);
  expect((margin.body as { error?: string }).error).toContain("you do not decide MARGIN_BELOW_MINIMUM approvals");
});

test("cost is visible: the receiving difference carries its still-open value", async ({ page }) => {
  const bearer = await mint(page, "yard-manager");
  const worklist = await staffGet(page, bearer, `/api/v1/purchasing/receiving?branch_id=${TRENTON}`);
  expect(worklist.status).toBe(200);
  const items = (worklist.body as { items?: Array<{ status?: string; value_open_cents?: number }> }).items ?? [];
  const difference = items.find((i) => i.status === "PARTIAL");
  expect(difference).toBeDefined();
  // The seed's one short line: 2 CYD of mulch at $27.00 buying, $54.00 still open.
  expect(difference?.value_open_cents).toBe(5400);
});

test("the branch grant reaches Trenton only: the dock lists Trenton's orders", async ({ page }) => {
  const bearer = await mint(page, "yard-manager");
  const worklist = await staffGet(page, bearer, `/api/v1/purchasing/receiving?branch_id=${TRENTON}`);
  expect(worklist.status).toBe(200);
  const items = (worklist.body as { items?: Array<{ branch_id?: string }> }).items ?? [];
  for (const item of items) expect(item.branch_id).toBe(TRENTON);
});
