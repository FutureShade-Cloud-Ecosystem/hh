// Round 10 item 23 (row 1092): the related documents and the counter payment are on the quote page where a person
// sees them, not hidden inside the collapsed "Quote detail & admin" bar; and that bar does not show on a page that
// has no quote to detail (an unreadable id, a New quote).
import { expect, test } from "@playwright/test";
import { showList, signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a converted quote shows its related documents and its payment without opening the bar", async ({ page }) => {
  await signIn(page);
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-2101");
  expect(await page.getByTestId("qb-detail").getAttribute("open")).toBeNull();
  await expect(page.locator("qd-related-docs").getByTestId("rd-box")).toBeVisible();
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toBeVisible();
  // They stand above the collapsed bar, not inside it.
  const docs = await page.locator("qd-related-docs").boundingBox();
  const bar = await page.getByText("Quote detail & admin").boundingBox();
  expect(docs !== null && bar !== null && docs.y < bar.y).toBe(true);
});

test("the Quote detail bar is not on a New quote page or an unreadable quote's page", async ({ page }) => {
  await signIn(page);
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  await expect(page.getByText("Quote detail & admin")).toBeHidden();
  await page.evaluate(() => {
    window.location.hash = "#/quotes/no-such-quote";
  });
  await expect(page.getByText(/could not be opened|may not exist|no longer exists/i).first()).toBeVisible();
  await expect(page.getByText("Quote detail & admin")).toBeHidden();
});
