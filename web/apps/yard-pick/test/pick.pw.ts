// Yard Pick, sub item 3: a pick in progress. Record what was pulled, line
// by line, with the reason a short line needs, and change the skid count on
// the skid stepper with a reason.
import { test, expect } from "@playwright/test";
import { resetStub, stubLog, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

/** Opens the claimed pick and starts it: the state the record doors need. */
async function openInProgress(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
  await page.getByRole("button", { name: "Start picking" }).click();
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
}

test("a claimed pick opens, starts, and shows its pull list", async ({ page }) => {
  await openInProgress(page);
  await expect(page.locator("[data-pull-list]")).toContainText("Pull list · 4 lines");
  await expect(page.locator("[data-line]")).toHaveCount(4);
  await expect(page.locator("[data-line]", { hasText: "Granular A gravel" })).toContainText("10 TONNE");
  await expect(page.getByRole("button", { name: "Save pick" })).toBeVisible();
});

test("a line opens the quantity editor: steppers, All, None, and the number holds", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
  await expect(page.locator("[data-qty-edit]")).toBeVisible();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("2");
  await page.getByRole("button", { name: "None" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("0");
  await page.getByRole("button", { name: "All 10" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("10");
  // The number holds through a re-render that opens another line.
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("10");
});

test("recording a full pick says so, and the line carries its tick", async ({ page }) => {
  await openInProgress(page);
  for (const what of ["Granular A gravel", "Granular B gravel", "Geotextile fabric", "Corrugated pipe"]) {
    await page.locator("[data-line]", { hasText: what }).locator(".line-row").click();
    await page.getByRole("button", { name: /^All \d+/ }).click();
  }
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("All 4 lines pulled in full.");
  await expect(page.locator("[data-pick-summary]")).toContainText("All 4 lines pulled in full.");
});

test("a short line needs its reason before the save, and the server backorders it", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  // Four of ten pulled: the reason select is offered and the save is refused without one.
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-error]")).toContainText("A short line needs the reason.");
  await page.getByLabel("Why line 1 is short").selectOption("NOT_IN_YARD");
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Picked 4 of 10 TONNE. The rest (6 TONNE) is on backorder");
  const line = page.locator("[data-line]", { hasText: "Granular A gravel" });
  await expect(line).toContainText("6 TONNE on backorder");
  await expect(line).toContainText("Not in the yard (NOT_IN_YARD)");
});

test("a quantity above what was ordered is refused in the server's own words", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.locator("[data-field=qty]").fill("3");
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-error]")).toContainText("that is more than was ordered, the yard cannot raise a quantity");
});

test("the skid stepper changes the count with a reason, and the answer is the server's own", async ({ page }) => {
  await openInProgress(page);
  await expect(page.locator("[data-skids]")).toBeVisible();
  await expect(page.locator("[data-skid-count]")).toHaveText("··");
  // The first save without a reason is refused before the round trip.
  await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the reason for the skid count.");
  await page.getByLabel("The reason for the skid count").selectOption("COUNT_CORRECTION");
  await page.getByRole("button", { name: "One skid more" }).click();
  await expect(page.locator("[data-skid-count]")).toHaveText("2");
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("SO-1040 now holds 2 skids.");
  // A change names both numbers, and the deposit figures never show.
  await page.getByLabel("The reason for the skid count").selectOption("CONDENSED_SKIDS");
  for (let i = 0; i < 3; i += 1) await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("SO-1040 changed from 2 to 5 skids.");
});

test("the record goes to the server as one call, in the ticket's own order", async ({ page, request }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("pulled");
  const calls = (await stubLog(request)).filter((c) => c.path.endsWith("/lines"));
  expect(calls).toHaveLength(1);
  expect(calls[0]?.body?.lines).toEqual([
    { order_line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000c1", sku: "GRAN-A", description: "Granular A gravel, bulk", qty_ordered: "10.0000", qty_picked: "10", uom: "TONNE" },
    { order_line_no: 3, product_id: "1a000000-0000-4000-9000-0000000000c3", sku: "FAB-GEO", description: "Geotextile fabric, 4 by 300 ft", qty_ordered: "2.0000", qty_picked: "2", uom: "ROLL" },
  ]);
});
