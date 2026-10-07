// Round 11 item 2 (the real server): the demo core's catalogue holds two categories, Pavers and Bulk, and the
// categories read says so. The Rules filter and the pickers on the rules and breaks tabs offered six built-in names
// (four of them not in the catalogue), so the margin tab said "every category has a policy" beside a Rules filter that
// listed more. Every category list on the Pricing screens is the catalogue read.
import { expect, test } from "@playwright/test";
import { openPricing, openTab, setMode, signIn } from "./pricing-helpers";

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const BULK = "ca700000-0000-4000-9000-000000000006";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a catalogue of two categories gives two on every Pricing screen", async ({ page }) => {
  await setMode(page, { catalogue: [PAVERS, BULK], drop_policies: [] });
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await openTab(page, "rules");
  const filter = page.getByTestId("pr-filter-category");
  await expect(filter.locator("option:not([value=''])")).toHaveText(["Bulk", "Pavers"]);
  await openTab(page, "breaks");
  await page.getByTestId("pb-new").click();
  await expect(page.locator('select[data-field="categoryId"]')).toHaveCount(1);
  await expect(page.locator('select[data-field="categoryId"] option')).toHaveText(["Bulk", "Pavers"]);
  // The form's default is a category the catalogue has.
  const value = await page.locator('select[data-field="categoryId"]').inputValue();
  expect([PAVERS, BULK]).toContain(value);
});
