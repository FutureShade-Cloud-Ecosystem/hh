// The quantity breaks tab in a real browser, against the stubbed harness: the schedules,
// the CP0-8 note and the "not applied" mark on every surcharge tier (nothing deleted), the
// New and Edit dialog with its tier list, a save through the editors' floor warning, the
// form's own refusals, a stale version, and Retire.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, pricingState, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const WALLS = "ca700000-0000-4000-9000-000000000002";

async function openBreaks(page: Page): Promise<void> {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "breaks");
  await expect(page.getByTestId("pb-schedule")).toHaveCount(2);
}

test("the schedules list with their tiers, and the product one is named", async ({ page }) => {
  await openBreaks(page);
  await expect(page.getByTestId("pb-count")).toHaveText("2 schedules");
  const natstone = page.getByTestId("pb-schedule").filter({ hasText: "NATSTONE-PARTIAL-SKID" });
  await expect(natstone).toContainText("product · GLS-BLUFF-STEP · Bluff Natural Step · per KG");
  await expect(natstone.getByTestId("pb-tier")).toContainText("0 KG");
  await expect(natstone.getByTestId("pb-tier")).toContainText("2000 KG");
  await expect(natstone.getByTestId("pb-tier")).toContainText("+10.00%");
  const pavers = page.getByTestId("pb-schedule").filter({ hasText: "PAVER-FULL-SKID" });
  await expect(pavers).toContainText("category · Pavers · per PLT");
  await expect(pavers.getByTestId("pb-tier")).toContainText("-5.00%");
  await expect(pavers.getByTestId("pb-tier")).toContainText("∞");
});

test("CP0-8: a note says surcharge tiers never change a price, and each such tier is marked not applied", async ({ page }) => {
  await openBreaks(page);
  const note = page.getByTestId("pb-surcharge-note");
  await expect(note).toContainText("Surcharge tiers never change a price");
  await expect(note).toContainText("only when it is cheaper");
  await expect(note).toContainText("nothing is deleted");
  await expect(note).toContainText("1 schedule below holds such a tier.");
  const natstone = page.getByTestId("pb-schedule").filter({ hasText: "NATSTONE-PARTIAL-SKID" });
  await expect(natstone.getByTestId("pb-not-applied")).toHaveText("not applied");
  await expect(natstone.getByText("surcharge", { exact: true })).toBeVisible();
  const pavers = page.getByTestId("pb-schedule").filter({ hasText: "PAVER-FULL-SKID" });
  await expect(pavers.getByTestId("pb-not-applied")).toHaveCount(0);
  await expect(pavers.getByText("discount", { exact: true })).toBeVisible();
  await shot(page.locator("qd-pricing-breaks"), "pricing-quantity-breaks");
});

test("the note and the mark are on the editor too, and a discount tier carries no mark", async ({ page }) => {
  await openBreaks(page);
  await page.getByTestId("pb-schedule").filter({ hasText: "NATSTONE-PARTIAL-SKID" }).getByTestId("pb-edit").click();
  await expect(page.getByTestId("pb-dialog-note")).toContainText("Surcharge tiers never change a price");
  await expect(page.getByTestId("pb-tier-mark")).toHaveText("Not applied: this tier never changes a price.");
  await expect(page.getByTestId("pb-tier-row")).toHaveCount(1);
  await shot(page.getByTestId("pb-dialog"), "pricing-quantity-break-editor");
  await page.getByTestId("pb-add-tier").click();
  await expect(page.getByTestId("pb-tier-row")).toHaveCount(2);
  await expect(page.getByTestId("pb-tier-mark")).toHaveCount(1);
  await page.locator('select[data-field="tier-valueType-1"]').selectOption("SURCHARGE_ABS");
  await expect(page.getByTestId("pb-tier-mark")).toHaveCount(2);
  await page.locator('select[data-field="tier-valueType-1"]').selectOption("ABS_OFF");
  await expect(page.getByTestId("pb-tier-mark")).toHaveCount(1);
});

test("nothing is deleted: saving the surcharge schedule again keeps its surcharge tier", async ({ page }) => {
  await openBreaks(page);
  await page.getByTestId("pb-schedule").filter({ hasText: "NATSTONE-PARTIAL-SKID" }).getByTestId("pb-edit").click();
  await page.locator('input[data-field="name"]').fill("Natural stone, partial skid");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-dialog")).toHaveCount(0);
  const state = await pricingState(page);
  const saved = state.breaks.find((b) => b.code === "NATSTONE-PARTIAL-SKID") as { name: string; items: Array<{ value_type: string; surcharge_bps: number }> };
  expect(saved.name).toBe("Natural stone, partial skid");
  expect(saved.items).toEqual([{ from_qty: 0, to_qty: 2000, value_type: "SURCHARGE_PCT", surcharge_bps: 1000, price_uom: "KG" }]);
  await expect(page.getByTestId("pb-not-applied")).toHaveCount(1);
});

test("a surcharge only schedule changes no price, so it saves with no floor warning", async ({ page }) => {
  await openBreaks(page);
  await page.getByTestId("pb-new").click();
  await page.locator('input[data-field="code"]').fill("WALL-PARTIAL");
  await page.locator('input[data-field="name"]').fill("Walls, partial skid");
  await page.locator('select[data-field="categoryId"]').selectOption(WALLS);
  await page.locator('select[data-field="tier-valueType-0"]').selectOption("SURCHARGE_PCT");
  await page.locator('input[data-field="tier-fromQty-0"]').fill("1");
  await page.locator('input[data-field="tier-valueInput-0"]').fill("25");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pb-schedule")).toHaveCount(3);
  expect((await pricingState(page)).confirmations).toHaveLength(0);
  await expect(page.getByTestId("pb-surcharge-note")).toContainText("2 schedules below hold such tiers.");
});

test("a deep discount tier warns before saving; declining saves nothing, confirming saves and logs", async ({ page }) => {
  await openBreaks(page);
  const fill = async () => {
    await page.getByTestId("pb-new").click();
    await page.locator('input[data-field="code"]').fill("PAVER-HALF");
    await page.locator('input[data-field="name"]').fill("Half off a skid");
    await page.locator('select[data-field="categoryId"]').selectOption(PAVERS);
    await page.locator('input[data-field="tier-fromQty-0"]').fill("1");
    await page.locator('input[data-field="tier-valueInput-0"]').fill("50");
  };
  await fill();
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("floor-headline")).toHaveText("This price puts 2 products under their margin floor.");
  await page.getByTestId("floor-decline").click();
  await expect(page.getByTestId("pb-dialog")).toBeVisible();
  expect((await pricingState(page)).breaks).toHaveLength(2);
  expect((await pricingState(page)).confirmations).toHaveLength(0);

  await page.getByTestId("pb-save").click();
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("pb-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pb-schedule")).toHaveCount(3);
  const state = await pricingState(page);
  const made = state.breaks.find((b) => b.code === "PAVER-HALF");
  expect(state.confirmations[0]).toMatchObject({ entity_type: "quantity_break", entity_id: made?.id, ref_entity_type: "category", ref_entity_id: PAVERS });
});

test("the form refuses what it can see is wrong, in words", async ({ page }) => {
  await openBreaks(page);
  await page.getByTestId("pb-new").click();
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-form-error")).toHaveText("Code and name are required.");
  await page.locator('input[data-field="code"]').fill("C");
  await page.locator('input[data-field="name"]').fill("N");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-form-error")).toContainText("Tier 1: enter a percentage");
  await page.locator('select[data-field="scopeKind"]').selectOption("product");
  await page.locator('input[data-field="tier-valueInput-0"]').fill("5");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-form-error")).toContainText("product id as a uuid");
  expect((await pricingState(page)).previews).toHaveLength(0);
});

test("a stale version is refused by the server and shown; the code in use is refused too", async ({ page }) => {
  await openBreaks(page);
  const state0 = await pricingState(page);
  const pavers = state0.breaks.find((b) => b.code === "PAVER-FULL-SKID");
  await page.getByTestId("pb-schedule").filter({ hasText: "PAVER-FULL-SKID" }).getByTestId("pb-edit").click();
  await page.request.post("/_stub/pricing-bump", { data: { id: pavers?.id } });
  await page.locator('input[data-field="name"]').fill("Pavers, skid");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-form-error")).toHaveText("Version conflict");
  await page.getByRole("button", { name: "Cancel" }).click();

  await page.getByTestId("pb-new").click();
  await page.locator('input[data-field="code"]').fill("PAVER-FULL-SKID");
  await page.locator('input[data-field="name"]').fill("Again");
  await page.locator('input[data-field="tier-valueInput-0"]').fill("1");
  await page.getByTestId("pb-save").click();
  await expect(page.getByTestId("pb-form-error")).toHaveText("A quantity break with this code already exists");
});

test("Retire asks first and keeps the schedule, greyed", async ({ page }) => {
  await openBreaks(page);
  await page.getByTestId("pb-schedule").filter({ hasText: "PAVER-FULL-SKID" }).getByTestId("pb-retire").click();
  await expect(page.getByTestId("pb-confirm")).toContainText("history is kept");
  await page.getByTestId("pb-retire-confirm").click();
  await expect(page.getByTestId("pb-confirm")).toHaveCount(0);
  await expect(page.getByTestId("pb-schedule").filter({ hasText: "PAVER-FULL-SKID" })).toHaveClass(/inactive/);
});
