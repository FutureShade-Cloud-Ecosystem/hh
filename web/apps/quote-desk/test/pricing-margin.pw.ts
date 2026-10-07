// The category margin policy tab in a real browser, against the stubbed harness: the six
// rows, the Save that arms when a row is dirty, a save that goes through the editors'
// floor warning (decline: nothing saved; confirm: saved and logged), a failed save shown
// in the row, and one row's save leaving another row's half typed edit alone.
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

test("the Sales manager sees the six categories with target, floor and enforcement", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.getByTestId("pm-category").first()).toHaveText("Pavers");
  const row = page.locator('tr[data-category-id="' + PAVERS + '"]');
  await expect(row.locator('input[data-field="target-' + PAVERS + '"]')).toHaveValue("33.00");
  await expect(row.locator('input[data-field="floor-' + PAVERS + '"]')).toHaveValue("20.00");
  await expect(row.locator('input[type="checkbox"]')).toBeChecked();
  await expect(page.locator("qd-pricing-margin .count")).toHaveText("6 categories");
  await shot(page.locator("qd-pricing-margin"), "pricing-margin-policy");
});

test("a clean row's Save is quiet and a changed row's Save arms", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  const save = page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save");
  await expect(save).not.toHaveClass(/primary/);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await expect(save).toHaveClass(/primary/);
});

test("a save that puts nothing under the floor goes straight through and is shown as saved", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  const state = await pricingState(page);
  expect(state.policies.find((p) => p.category_id === PAVERS)).toMatchObject({ margin_target_bps: 3400, margin_floor_bps: 2000 });
  expect(state.confirmations).toHaveLength(0);
});

test("raising the floor shows the products it would put under it, and declining saves nothing", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("45");
  await page.locator('input[data-field="floor-' + PAVERS + '"]').fill("40");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  const dialog = page.getByTestId("floor-dialog");
  await expect(dialog).toBeVisible();
  await expect(page.getByTestId("floor-headline")).toHaveText("This price puts 2 products under their margin floor.");
  await expect(page.getByTestId("floor-item")).toHaveCount(2);
  await expect(page.getByTestId("floor-item").first()).toContainText("HPV-80");
  // The change named no branch, so the check graded every yard: the worst item names the yard
  // it happened at by its code, with the full name in the line's tooltip (Kingston runs the
  // dearer pavers cost), never a yard-less figure.
  await expect(page.getByTestId("floor-item").first()).toContainText("for Retail at KGN");
  await expect(page.getByTestId("floor-item").first()).toHaveAttribute("title", "Kingston");
  await shot(dialog, "pricing-floor-warning");
  await page.getByTestId("floor-decline").click();
  await expect(dialog).toHaveCount(0);
  const state = await pricingState(page);
  expect(state.policies.find((p) => p.category_id === PAVERS)).toMatchObject({ margin_floor_bps: 2000 });
  expect(state.confirmations).toHaveLength(0);
  expect(state.writes).toHaveLength(0);
});

test("a floor raised with no branch named still meets a rule limited to one branch", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  // The seeded contractor default rule is limited to Trenton (15 percent off), so a
  // branch-less check that ignores branch rules would grade the wall block at its list
  // price and name nothing at a 25 percent floor. Graded per branch, Trenton's own rule
  // prices it at 765 (20.26 percent) and the item names the yard.
  const walls = "ca700000-0000-4000-9000-000000000002";
  await page.locator('input[data-field="floor-' + walls + '"]').fill("25");
  await page.locator('tr[data-category-id="' + walls + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await expect(page.getByTestId("floor-item")).toHaveCount(1);
  await expect(page.getByTestId("floor-item").first()).toContainText("WALL-12");
  await expect(page.getByTestId("floor-item").first()).toContainText("for Contractor at TRT");
  await expect(page.getByTestId("floor-item").first()).toContainText("price $7.65 per ea, unchanged");
});

test("confirming saves the policy and logs the confirmation against it", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("45");
  await page.locator('input[data-field="floor-' + PAVERS + '"]').fill("40");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  const state = await pricingState(page);
  expect(state.policies.find((p) => p.category_id === PAVERS)).toMatchObject({ margin_target_bps: 4500, margin_floor_bps: 4000 });
  expect(state.confirmations).toHaveLength(1);
  expect(state.confirmations[0]).toMatchObject({ entity_type: "category_policy", entity_id: PAVERS, ref_entity_type: "category", ref_entity_id: PAVERS });
  expect((state.confirmations[0]?.product_ids as string[]).length).toBe(2);
  expect(state.audit.some((r) => r.field_name === "floor_warning_confirmed" && r.entity_id === PAVERS)).toBe(true);
});

test("a floor above the target is said in the row and nothing is sent", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="floor-' + PAVERS + '"]').fill("50");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-error")).toHaveText("The floor must be at or below the target.");
  expect((await pricingState(page)).previews).toHaveLength(0);
});

test("a save the server refuses is shown in the row, never silently", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await setMode(page, { fail_next: { kind: "save", status: 500, message: "pricing: the margin policy could not be written" } });
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-error")).toHaveText("The margin policy could not be written");
  await expect(page.getByTestId("pm-saved")).toHaveCount(0);
  expect((await pricingState(page)).policies.find((p) => p.category_id === PAVERS)).toMatchObject({ margin_target_bps: 3300 });
});

test("saving one row keeps another row's half typed edit", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  const walls = "ca700000-0000-4000-9000-000000000002";
  await page.locator('input[data-field="target-' + walls + '"]').fill("31.5");
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  await expect(page.locator('input[data-field="target-' + walls + '"]')).toHaveValue("31.5");
});

test("the audit link opens this tab with the category's row marked", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  await openTab(page, "audit");
  await page.getByTestId("pa-link").first().click();
  await expect(page.getByTestId("pricing-tab-margin")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator('tr[data-category-id="' + PAVERS + '"]')).toHaveClass(/focused/);
});
