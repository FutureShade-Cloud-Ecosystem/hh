// PR 123's Pricing desk tab affordance below the shared header (decision log
// row 977): Pricing is reachable as a desk tab, not a chrome button, and the
// Pricing tab is manager only.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signInAs(page: Page, role: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("a manager sees Quotes and Pricing desk tabs and can open Pricing", async ({ page }) => {
  await signInAs(page, "Sales manager");
  const tabs = page.getByTestId("desk-tabs");
  await expect(tabs).toBeVisible();
  await expect(page.getByTestId("desk-tab-quotes")).toBeVisible();
  const pricingTab = page.getByTestId("desk-tab-pricing");
  await expect(pricingTab).toBeVisible();
  await pricingTab.click();
  await expect(page.getByTestId("pricing-screen")).toBeVisible();
  // Switching back to Quotes hides the pricing panel.
  await page.getByTestId("desk-tab-quotes").click();
  await expect(page.getByTestId("pricing-screen")).toHaveCount(0);
});

test("a salesperson sees only the Quotes desk tab", async ({ page }) => {
  await signInAs(page, "Sales");
  const tabs = page.getByTestId("desk-tabs");
  await expect(tabs).toBeVisible();
  await expect(page.getByTestId("desk-tab-quotes")).toBeVisible();
  await expect(page.getByTestId("desk-tab-pricing")).toHaveCount(0);
});
