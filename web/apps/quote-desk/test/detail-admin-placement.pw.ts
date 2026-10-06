// The placement of the quote's secondary jobs, as the plan decides it (QD-0-plan-r1.md) and as decision log rows
// 955 and 957 reshape it: nothing stacks under the quote as a disclosure ("Quote detail & admin" is gone). The plan
// puts the history, follow ups, assist and availability where the Sales desk reads them, and the per line edit
// panel and Check Quote (the manager's admin work) where only the Sales manager does:
//
//   Activity (history, follow ups, assist) and Stock (availability, hold expiry): Sales and Sales manager
//   Admin (the per line edit panel, Check Quote): Sales manager only; for Sales the tab and its panels are not
//   in the page, and the route says "no access"
//
// The role gate inside each panel is still the server's own offers (editable_fields); this pins where each panel
// stands and who reaches it. QD-6 builds the way in for exactly two logins, Sales and Sales manager.
import { expect, test, type Page } from "@playwright/test";
import { openTab } from "./surface-tabs";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function openQuoteAs(page: Page, role: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
}

test("the Sales desk has Activity and Stock, and no Admin: its panels are not in the page", async ({ page }) => {
  await openQuoteAs(page, "Sales");
  await expect(page.getByText("Quote detail & admin")).toHaveCount(0);
  await openTab(page, "activity");
  await expect(page.locator("qd-quote-history")).toBeVisible();
  await openTab(page, "stock");
  await expect(page.locator("qd-quote-availability")).toBeVisible();
  await expect(page.getByTestId("qt-tab-admin")).toHaveCount(0);
  await expect(page.locator("qd-quote-edit, qd-quote-guard")).toHaveCount(0);
});

test("the Sales manager desk has Admin too, the demo's administrator-facing surface", async ({ page }) => {
  await openQuoteAs(page, "Sales manager");
  await expect(page.getByText("Quote detail & admin")).toHaveCount(0);
  await openTab(page, "admin");
  await expect(page.locator("qd-quote-edit")).toBeVisible();
  await expect(page.locator("qd-quote-guard")).toBeVisible();
});
