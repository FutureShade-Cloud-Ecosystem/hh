// QD-4: one deterministic test for each guard of the pricing panel. Each holds one answer back behind a
// delayed route, closes the panel (the Quotes desk tab swaps it away, unmounting the tab and cancelling
// its context) or signs out, lets the answer go, and asserts none of it lands: no follow-up request from
// the dead panel, no confirmation write under a session that has ended, no late list over a newer one.
// Take the guard away (test/mutants.json names the exact edit) and the test goes red.
import { expect, test, type Page } from "@playwright/test";
import { openAccountMenu } from "./account-menu";
import { countRequests, hold, quiet, settle } from "./switch-helpers.js";
import { openPricing, openTab, pricingState, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const WALLS = "ca700000-0000-4000-9000-000000000002";
const SLABS = "ca700000-0000-4000-9000-000000000003";
const BULK = "ca700000-0000-4000-9000-000000000006";
const MAPLE = "c0000000-0000-4000-9000-000000000001";

/** A new tier rule on Pavers at a depth that leaves a margin: the form filled, the Save pressed. */
async function saveNewPaversRule(page: Page, percent: string): Promise<void> {
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill(percent);
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
}

/** The Quotes desk tab leaves the pricing screen the way a dispatched click does, past the open backdrop. */
async function closePricing(page: Page): Promise<void> {
  await page.getByTestId("desk-tab-quotes").dispatchEvent("click");
  await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
}

async function signOutAway(page: Page): Promise<void> {
  await openAccountMenu(page, "dispatch");
  await page.getByRole("button", { name: "Sign out" }).dispatchEvent("click");
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
}

/** Ask the panel to re-read after it has been taken off the page, as the builder's fan-out would. */
async function reloadRemovedPanel(page: Page): Promise<void> {
  await page.locator("qd-pricing-panel").evaluate((el) => {
    el.remove();
    (el as HTMLElement & { reload: () => void }).reload();
  });
}

// ── the panel that has left the page ──

test("P3-1 a pricing tab that has left the page reads nothing when it is asked to reload", async ({ page }) => {
  await signIn(page);
  for (const tab of ["margin", "rules", "customers", "breaks", "audit"] as const) {
    await openPricing(page);
    await openTab(page, tab);
    if (tab === "customers") {
      // The tab reads only for a customer: give it one the way an audit link would.
      await page.locator("qd-pricing-customers").evaluate((el, id) => {
        (el as HTMLElement & { focusCustomerId: string }).focusCustomerId = id;
      }, MAPLE);
      await expect(page.getByTestId("pc-system")).toBeVisible();
    }
    await quiet(page);
    // The offline layer's sync pull ticks about once a second (four reads of /api/v1/sync/*) whatever the
    // panel does; it is the app's own poll, not a read of the dead panel, and it landing inside this window
    // failed the leg on a slow runner. The panel's own reads (pricing, products, customers) are all counted.
    const seen = countRequests(page, /\/api\/(?!v1\/sync\/)/);
    const before = seen();
    await reloadRemovedPanel(page);
    await settle(page);
    expect(seen(), `${tab} asked a dead panel's read`).toBe(before);
    // Put the desk back the way the Quotes tab leaves it, so the next part opens its own panel.
    await page.getByTestId("desk-tab-quotes").click();
    await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
  }
});

// ── the order of reads within one tab ──

/** The one guard probe of a late refused read, per tab: the read asked second answers first, the first's refusal lands last and must not clear it. */
async function lateRefusedRead(page: Page, c: { tab: string; url: RegExp; error: string; rows: string; count: number }): Promise<void> {
  await signIn(page);
  await openPricing(page);
  await openTab(page, c.tab as "margin" | "rules" | "customers" | "breaks" | "audit");
  if (c.tab === "customers") {
    await page.locator("qd-pricing-customers").evaluate((el, id) => {
      (el as HTMLElement & { focusCustomerId: string }).focusCustomerId = id;
    }, MAPLE);
  }
  await expect(page.getByTestId(c.rows)).toHaveCount(c.count);
  await quiet(page);
  const refused = await hold(page, c.url, { method: "GET", times: 1, refuse: { status: 500, error: "the list could not be read" } });
  await page.locator("qd-pricing-panel").evaluate((el) => (el as HTMLElement & { reload: () => void }).reload());
  await refused.asked;
  // The read asked second answers first, and the tab shows it.
  await page.locator("qd-pricing-panel").evaluate((el) => (el as HTMLElement & { reload: () => void }).reload());
  await expect(page.getByTestId(c.rows)).toHaveCount(c.count);
  await refused.release();
  await settle(page);
  await expect(page.getByTestId(c.rows)).toHaveCount(c.count);
  await expect(page.getByTestId(c.error)).toHaveCount(0);
}

test("P3-1 an older refused pricing read answered late: margin policy", async ({ page }) => {
  await lateRefusedRead(page, { tab: "margin", url: /\/api\/v1\/pricing\/category-policies/, error: "pm-load-error", rows: "pm-row", count: 6 });
});

test("P3-1 an older refused pricing read answered late: rules", async ({ page }) => {
  await lateRefusedRead(page, { tab: "rules", url: /\/api\/v1\/pricing\/rules/, error: "pr-error", rows: "pr-row", count: 5 });
});

test("P3-1 an older refused pricing read answered late: customer prices", async ({ page }) => {
  await lateRefusedRead(page, { tab: "customers", url: /\/api\/v1\/pricing\/customers\/[^/]+\/overrides/, error: "pc-error", rows: "pc-system", count: 1 });
});

test("P3-1 an older refused pricing read answered late: quantity breaks", async ({ page }) => {
  await lateRefusedRead(page, { tab: "breaks", url: /\/api\/v1\/pricing\/quantity-breaks/, error: "pb-error", rows: "pb-schedule", count: 2 });
});

test("P3-1 an older refused pricing read answered late: the pricing log", async ({ page }) => {
  await lateRefusedRead(page, { tab: "audit", url: /\/api\/v1\/pricing\/audit/, error: "pa-error", rows: "pa-empty", count: 1 });
});

// ── a read that answers after the panel closed ──

test("P3-1 a pricing read that answers after the panel closed sends no name or roster lookup: the breaks list", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  // The margin tab reads the breaks too (for the Add picker); settle that read, then hold the breaks tab's own.
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await quiet(page);
  const held = await hold(page, /\/api\/v1\/pricing\/quantity-breaks\?/, { method: "GET", times: 1 });
  await page.getByTestId("pricing-tab-breaks").click();
  await held.asked;
  await closePricing(page);
  const lookups = countRequests(page, /\/api\/v1\/(products|staff)/);
  await held.release();
  await settle(page);
  expect(lookups()).toBe(0);
});

test("P3-1 a pricing read that answers after the panel closed sends no name or roster lookup: the pricing log", async ({ page }) => {
  await signIn(page);
  const held = await hold(page, /\/api\/v1\/pricing\/audit\?/, { method: "GET", times: 1 });
  await openPricing(page);
  await page.getByTestId("pricing-tab-audit").click();
  await held.asked;
  await closePricing(page);
  const lookups = countRequests(page, /\/api\/v1\/(products|staff)/);
  await held.release();
  await settle(page);
  expect(lookups()).toBe(0);
});

// ── the floor flow's writes ──

test("P3-1 a floor check that answers after the panel closed saves nothing", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  const held = await hold(page, /\/api\/v1\/pricing\/floor-preview$/, { method: "POST", times: 1 });
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await held.asked;
  await closePricing(page);
  await held.release();
  await settle(page);
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(0);
  expect(state.policies.find((p) => p.category_id === PAVERS)).toMatchObject({ margin_target_bps: 3300 });
});

test("P3-1 a confirmed save that lands after the panel closed logs no confirmation from the dead panel", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await saveNewPaversRule(page, "40");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  const held = await hold(page, /\/api\/v1\/pricing\/rules$/, { method: "POST", times: 1 });
  await page.getByTestId("floor-confirm").click();
  await held.asked;
  await closePricing(page);
  await held.release();
  await settle(page);
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(1);
  expect(state.confirmations).toHaveLength(0);
});

// ── the Add category row ──

test("P3-1 the Add category save is bound to the category and numbers picked when it began", async ({ page }) => {
  await setMode(page, { drop_policies: [WALLS, SLABS], rule_categories: [WALLS, SLABS] });
  await signIn(page);
  await openPricing(page);
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("20");
  const held = await hold(page, /\/api\/v1\/pricing\/floor-preview$/, { method: "POST", times: 1 });
  await page.getByTestId("pm-add-save").click();
  await held.asked;
  // The picker moves on while the floor check is out; the save still goes for the category it began with.
  await page.getByTestId("pm-add-category").selectOption(SLABS);
  await held.release();
  await settle(page);
  const state = await pricingState(page);
  expect(state.policies.find((p) => p.category_id === WALLS)).toMatchObject({ margin_target_bps: 3000, margin_floor_bps: 2000 });
  expect(state.policies.find((p) => p.category_id === SLABS)).toBeUndefined();
});

test("P3-1 an older read of the categories, answered late, does not shrink the Add picker", async ({ page }) => {
  await setMode(page, { drop_policies: [WALLS], catalogue: [PAVERS, BULK, WALLS] });
  await signIn(page);
  const held = await hold(page, /\/api\/v1\/product-categories(\?|$)/, { method: "GET", times: 1, snapshot: true });
  await openPricing(page);
  await held.asked;
  // The catalogue changes while the first read is out; the reload sees it, and the first read's older answer lands last.
  await setMode(page, { drop_policies: [SLABS], catalogue: [PAVERS, BULK, SLABS] });
  await page.locator("qd-pricing-panel").evaluate((el) => (el as HTMLElement & { reload: () => void }).reload());
  await expect(page.getByTestId("pm-add-category").locator("option:not([value=''])")).toHaveText(["Slabs & Steps"]);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("pm-add-category").locator("option:not([value=''])")).toHaveText(["Slabs & Steps"]);
});

// ── two quick Edit clicks ──

test("two quick Edit clicks on the customer prices tab open the later one", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "customers");
  await page.locator("qd-pricing-customers").evaluate((el, id) => {
    (el as HTMLElement & { focusCustomerId: string }).focusCustomerId = id;
  }, MAPLE);
  await expect(page.getByTestId("pc-system")).toBeVisible();
  // Two prices on two categories, so the two forms are told apart by their value.
  for (const [category, value] of [
    ["ca700000-0000-4000-9000-000000000001", "5"],
    ["ca700000-0000-4000-9000-000000000002", "8"],
  ] as const) {
    await page.getByTestId("pc-add").click();
    await page.getByTestId("pc-category").selectOption(category);
    await page.getByTestId("pc-value").fill(value);
    await page.getByTestId("pc-save").click();
    await expect(page.getByTestId("pc-dialog")).toHaveCount(0);
  }
  await expect(page.getByTestId("pc-row")).toHaveCount(2);
  await quiet(page);
  const first = page.getByTestId("pc-row").first();
  const second = page.getByTestId("pc-row").nth(1);
  const firstId = await first.getByTestId("pc-edit").getAttribute("data-id");
  const held = await hold(page, /\/api\/v1\/pricing\/customers\/[^/]+\/overrides/, {
    method: "GET",
    times: 1,
  });
  await first.getByTestId("pc-edit").click();
  await held.asked;
  await second.getByTestId("pc-edit").click();
  await expect(page.getByTestId("pc-dialog")).toBeVisible();
  await expect(page.getByTestId("pc-value")).toHaveValue("8.00");
  await held.release();
  await settle(page);
  await expect(page.getByTestId("pc-value")).toHaveValue("8.00");
  expect(firstId).not.toBeNull();
});

test("two quick Edit clicks on the quantity breaks tab open the later one", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "breaks");
  await expect(page.getByTestId("pb-schedule")).toHaveCount(2);
  await quiet(page);
  const first = page.getByTestId("pb-schedule").first();
  const second = page.getByTestId("pb-schedule").nth(1);
  const firstId = await first.getByTestId("pb-edit").getAttribute("data-id");
  const held = await hold(page, new RegExp(`/api/v1/pricing/quantity-breaks/${firstId}$`), { method: "GET", times: 1 });
  await first.getByTestId("pb-edit").click();
  await held.asked;
  await second.getByTestId("pb-edit").click();
  await expect(page.getByTestId("pb-dialog")).toBeVisible();
  const laterCode = await second.getAttribute("data-code");
  await expect(page.getByTestId("pb-code")).toHaveValue(laterCode ?? "");
  await held.release();
  await settle(page);
  await expect(page.getByTestId("pb-code")).toHaveValue(laterCode ?? "");
});

// ── sign out and the floor work still out ──

test("P2-2 sign out waits for a confirmed pricing save still out and its confirmation lands", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await saveNewPaversRule(page, "40");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  const held = await hold(page, /\/api\/v1\/pricing\/rules$/, { method: "POST", times: 1 });
  await page.getByTestId("floor-confirm").click();
  await held.asked;
  // Sign out starts while the confirmed save is still out: it waits, so the save and the confirmation
  // its answer owes both land while the session stands.
  await openAccountMenu(page, "dispatch");
  await page.getByRole("button", { name: "Sign out" }).dispatchEvent("click");
  await held.release();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible({ timeout: 10000 });
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(1);
  expect(state.confirmations).toHaveLength(1);
});

test("P2-2 sign out sends a floor confirmation the log write refused", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  // The first sign out press warns and holds; the second goes, and the wait sends the owed row.
  await openAccountMenu(page, "dispatch");
  await page.getByRole("button", { name: "Sign out" }).dispatchEvent("click");
  await expect(page.getByTestId("pricing-leave-warning")).toBeVisible();
  await signOutAway(page);
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(1);
  expect(state.confirmations).toHaveLength(1);
  expect(state.audit.some((r) => r.field_name === "floor_warning_confirmed")).toBe(true);
});
