// The rule editor tab in a real browser, against the stubbed harness: the list and its
// filters, the New rule and Edit dialog, the form's own refusals, a save through the
// editors' floor warning (decline: nothing saved; confirm: saved and logged), a stale
// version, a failed save, Retire, and the dialog's focus.
import { expect, test } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, pricingState, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";

async function openRules(page: import("@playwright/test").Page): Promise<void> {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-row")).toHaveCount(5);
}

test("the rules list reads as the console's: scope, applies to, value, priority", async ({ page }) => {
  await openRules(page);
  const first = page.getByTestId("pr-row").first();
  await expect(first).toContainText("Tier + category");
  await expect(first).toContainText("CONTRACTOR · Bulk");
  await expect(first).toContainText("0.00% off");
  await expect(first).toContainText("100");
  await expect(page.getByTestId("pr-row").last()).toContainText("Tier default");
  await expect(page.getByTestId("pr-count")).toHaveText("5 rules");
  await shot(page.locator("qd-pricing-rules"), "pricing-rules");
});

test("the filters narrow the list from the server", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-filter-scope").selectOption("TIER_DEFAULT");
  await expect(page.getByTestId("pr-row")).toHaveCount(1);
  await page.getByTestId("pr-filter-scope").selectOption("");
  await page.getByTestId("pr-filter-category").selectOption(PAVERS);
  await expect(page.getByTestId("pr-row")).toHaveCount(2);
  await expect(page.getByTestId("pr-row").first()).toContainText("Pavers");
  await page.getByTestId("pr-filter-category").selectOption("");
  await page.getByTestId("pr-filter-type").selectOption("RETAIL");
  await expect(page.getByTestId("pr-empty")).toBeVisible();
});

test("a new rule that puts nothing under the floor saves straight away and shows in the list", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  await page.getByTestId("pr-customer-type").selectOption("RETAIL");
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-detail").fill("Retail default");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-row")).toHaveCount(6);
  const state = await pricingState(page);
  const made = state.rules.find((r) => r.detail_text === "Retail default");
  expect(made).toMatchObject({ scope: "TIER_DEFAULT", customer_type: "RETAIL", discount_bps: 500, category_id: "" });
  expect(state.confirmations).toHaveLength(0);
});

test("the form refuses what it can see is wrong, in words, and sends nothing", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("Enter a percentage with at most two decimals.");
  await page.getByTestId("pr-value").fill("1.005");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("Enter a percentage with at most two decimals.");
  await page.getByTestId("pr-scope").selectOption("CUSTOMER_PRODUCT");
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toContainText("customer id as a uuid");
  expect((await pricingState(page)).previews).toHaveLength(0);
});

test("a fixed price is offered on a customer and product rule alone", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await expect(page.getByTestId("pr-calc").locator("option")).toHaveCount(2);
  await page.getByTestId("pr-scope").selectOption("CUSTOMER_PRODUCT");
  await expect(page.getByTestId("pr-calc").locator("option")).toHaveCount(3);
  await page.getByTestId("pr-calc").selectOption("ABSOLUTE");
  await expect(page.getByTestId("pr-price")).toBeVisible();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await expect(page.getByTestId("pr-calc")).toHaveValue("PCT_OFF_LIST");
  await expect(page.getByTestId("pr-price")).toHaveCount(0);
});

test("a rule that would put products under the floor is shown before saving, and declining saves nothing", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-customer-type").selectOption("CONTRACTOR");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await expect(page.getByTestId("floor-headline")).toHaveText("This price puts 2 products under their margin floor.");
  await expect(page.getByTestId("floor-item").first()).toContainText("price CA$3.12 per sf (was CA$4.26)");
  await page.getByTestId("floor-decline").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  const state = await pricingState(page);
  expect(state.rules).toHaveLength(5);
  expect(state.confirmations).toHaveLength(0);
  expect(state.writes).toHaveLength(0);
});

test("confirming saves the rule and the confirmation reaches the pricing log against it", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-customer-type").selectOption("CONTRACTOR");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-row")).toHaveCount(6);
  const state = await pricingState(page);
  const made = state.rules.find((r) => r.priority === 90);
  expect(made).toMatchObject({ discount_bps: 4000, category_id: PAVERS });
  expect(state.confirmations).toHaveLength(1);
  expect(state.confirmations[0]).toMatchObject({ entity_type: "pricing_rule", entity_id: made?.id, ref_entity_type: "category", ref_entity_id: PAVERS });
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-field").filter({ hasText: "Floor warning confirmed" })).toHaveCount(1);
  await expect(page.getByTestId("pa-change").filter({ hasText: "the editor confirmed a floor warning naming 2 products" })).toHaveCount(1);
});

test("a confirmation the log would not take is said, with a way to try again, and the saved rule stays saved", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-error")).toContainText("The price was saved, but the confirmation could not be logged");
  expect((await pricingState(page)).rules).toHaveLength(6);
  await page.getByTestId("floor-retry-log").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  const state = await pricingState(page);
  expect(state.rules).toHaveLength(6);
  expect(state.confirmations).toHaveLength(1);
});

test("a floor check that cannot run saves nothing and says so", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_next: { kind: "preview", status: 500, message: "pricing: the product catalogue could not be read" } });
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("The product catalogue could not be read. Nothing was saved, because the floor check could not run.");
  expect((await pricingState(page)).writes).toHaveLength(0);
});

test("a save the server refuses is shown in the dialog, never silently", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_next: { kind: "save", status: 422, message: "pricing: a conflicting active rule already exists" } });
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("A conflicting active rule already exists");
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  expect((await pricingState(page)).rules).toHaveLength(5);
});

test("Edit opens the rule fresh and a stale version is refused by the server and shown", async ({ page }) => {
  await openRules(page);
  const state0 = await pricingState(page);
  const pavers = state0.rules.find((r) => r.category_id === PAVERS);
  await page.getByTestId("pr-row").filter({ hasText: "Contractor, Pavers" }).getByTestId("pr-edit").click();
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  await expect(page.getByTestId("pr-value")).toHaveValue("18.00");
  await expect(page.getByTestId("pr-priority")).toHaveValue("40");
  await page.request.post("/_stub/pricing-bump", { data: { id: pavers?.id } });
  await page.getByTestId("pr-priority").fill("41");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("Version conflict");
  expect((await pricingState(page)).rules.find((r) => r.id === pavers?.id)).toMatchObject({ priority: 40 });
  // The next try opens the rule again and carries the version the server holds now.
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByTestId("pr-row").filter({ hasText: "Contractor, Pavers" }).getByTestId("pr-edit").click();
  await page.getByTestId("pr-priority").fill("41");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  expect((await pricingState(page)).rules.find((r) => r.id === pavers?.id)).toMatchObject({ priority: 41 });
});

test("Retire asks first, then retires and keeps the row, greyed", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-row").filter({ hasText: "Accessories" }).getByTestId("pr-retire").click();
  await expect(page.getByTestId("pr-confirm")).toContainText("history is kept");
  await page.getByRole("button", { name: "Cancel" }).click();
  expect((await pricingState(page)).rules.every((r) => r.is_active === true)).toBe(true);
  await page.getByTestId("pr-row").filter({ hasText: "Accessories" }).getByTestId("pr-retire").click();
  await page.getByTestId("pr-retire-confirm").click();
  await expect(page.getByTestId("pr-confirm")).toHaveCount(0);
  await expect(page.getByTestId("pr-row").filter({ hasText: "Accessories" })).toHaveClass(/inactive/);
  expect((await pricingState(page)).rules.filter((r) => r.is_active === false)).toHaveLength(1);
});

test("the dialog keeps focus inside, Escape closes it and focus goes back to the button", async ({ page }) => {
  await openRules(page);
  const open = page.getByTestId("pr-new");
  await open.click();
  await expect(page.getByTestId("pr-scope")).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(page.getByRole("button", { name: "Close" })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(page.getByTestId("pr-save")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Close" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  await expect(open).toBeFocused();
});

test("Escape on the floor warning is a decline: nothing is saved", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  expect((await pricingState(page)).writes).toHaveLength(0);
});

// A double click on the confirm button lands as one rule and one log row: the busy guard
// (pricing-tab-base) holds the second click before the button can disable. Two synchronous
// clicks inside one frame are the case, they both fire before a re-render.
test("a double click on the confirm saves one rule and one log row", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-customer-type").selectOption("CONTRACTOR");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-priority").fill("91");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await page.getByTestId("floor-confirm").evaluate((button) => {
    button.click();
    button.click();
  });
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-row")).toHaveCount(6);
  const state = await pricingState(page);
  expect(state.rules.filter((r) => r.priority === 91)).toHaveLength(1);
  expect(state.confirmations).toHaveLength(1);
  expect(state.writes.filter((w) => w.kind === "rule")).toHaveLength(1);
});
