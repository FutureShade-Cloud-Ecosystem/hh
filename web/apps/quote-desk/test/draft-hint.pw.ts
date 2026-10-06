// The draft line above Save (visual review round 1, P3): the ERP says what a
// draft is above the actions; the desk did not.
import { expect, test, type Page } from "@playwright/test";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a draft says what it is above Save, and a sent quote no longer does", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await newQuote(page);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const hint = page.getByTestId("qb-draft-hint");
  await expect(hint).toHaveText("A draft is a quote you have not sent yet.");
  const hintBox = (await hint.boundingBox())!;
  const saveBox = (await page.getByTestId("qb-save-draft").boundingBox())!;
  expect(hintBox.y + hintBox.height).toBeLessThanOrEqual(saveBox.y);
  await page.getByTestId("qb-send").click();
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
  await expect(hint).toHaveCount(0);
});
