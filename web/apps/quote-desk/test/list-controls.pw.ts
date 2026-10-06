// The quotes list page under the split-list navigation: its toolbar carries
// the design system's controls, not raw browser chrome. The demo round's
// defect 2 (a bare select, a native date input, plain buttons, a fieldset
// legend) stays closed on the new page: the search is the design system's
// text control, the status tabs are its segmented control, and the New quote
// button in the title row is its primary button.
import { expect, test, type Page } from "@playwright/test";

const SALES = { name: "Sales", exact: true };

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", SALES).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("the list page holds no bare native controls", async ({ page }) => {
  await signIn(page);
  const listPage = page.getByTestId("qd-list-page");
  await expect(listPage).toBeVisible();
  await expect(listPage.locator("select:not([data-control])")).toHaveCount(0);
  await expect(listPage.locator("input:not([data-control])")).toHaveCount(0);
  await expect(listPage.locator("fieldset:not([data-control])")).toHaveCount(0);
  // The toolbar's own buttons are the design system's (the Refresh button)
  // or the segmented control's segments; the rows' own buttons are the
  // page's content, the way the list's row buttons always were.
  await expect(page.getByTestId("qd-list-toolbar").locator("select")).toHaveCount(0);
});

test("the toolbar carries the segmented control and the search, the title row the primary New quote", async ({ page }) => {
  await signIn(page);
  const toolbar = page.getByTestId("qd-list-toolbar");
  await expect(toolbar).toBeVisible();
  const search = toolbar.locator('input[data-control="text"][data-field="list-search"]');
  await expect(search).toBeVisible();
  const tabs = toolbar.locator('[data-control="segmented"][data-field="status-filter"]');
  await expect(tabs).toBeVisible();
  for (const label of ["All", "Draft", "Sent", "Accepted", "Expired", "Converted"]) {
    await expect(tabs.getByRole("button", { name: label, exact: true })).toBeVisible();
  }
  // Sales sees neither of the manager's extra tabs.
  await expect(tabs.getByRole("button", { name: "Approvals", exact: true })).toHaveCount(0);
  const refresh = toolbar.locator('button.btn-secondary[data-action="refresh"]');
  await expect(refresh).toBeVisible();
  await expect(refresh).toHaveText("Refresh");
  const newQuote = page.locator('button.btn-primary[data-action="new-quote"]');
  await expect(newQuote).toBeVisible();
  await expect(newQuote).toHaveText("New quote");
});
