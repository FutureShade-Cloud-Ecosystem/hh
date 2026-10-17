// The front door's approvals note tests: the Quote Desk tile carries a quiet
// "N approvals waiting" note when the sales manager has pending margin requests,
// and the note is absent in all other states.
//
// The note is rendered by app-root.ts for the Quote Desk tile only when the
// signed-in role is sales-manager and the pending approval count is greater than
// zero. The count comes from GET /api/v1/staff/approvals with status PENDING,
// which the demo stub serves.
//
// The stub answers the contract's page object, over two pages when there is more
// than one row, and one leg mixes in rows the Quote Desk's Approvals tab does not
// list (another kind, and a decided margin row).
import { expect, test, type Page } from "@playwright/test";

async function stubSetApprovals(page: Page, count: number, more: { extra?: number; delayMs?: number } = {}): Promise<void> {
  const res = await page.request.post("/_stub/set-approvals", { data: { count, ...more } });
  expect(res.ok()).toBe(true);
}

async function stubReset(page: Page): Promise<void> {
  const res = await page.request.post("/_stub/reset");
  expect(res.ok()).toBe(true);
}

async function signInSalesManager(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
}

async function signInSales(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await stubReset(page);
  await page.goto("/");
});

test("sales manager with pending approvals sees the note on the Quote Desk tile", async ({ page }) => {
  // Stub three pending approvals.
  await stubSetApprovals(page, 3);
  await signInSalesManager(page);
  // The Quote Desk tile must contain the note.
  const quoteDeskTile = page.getByRole("button", { name: "Quote Desk", exact: true });
  await expect(quoteDeskTile).toBeVisible();
  await expect(quoteDeskTile).toContainText("3 approvals waiting");
  // The tile's heading and link text still render.
  await expect(quoteDeskTile).toContainText("Quote Desk");
  await expect(quoteDeskTile).toContainText("Size the job, build and send the quote.");
  await expect(quoteDeskTile).toContainText("Open the Quote Desk");
});

test("sales manager with zero pending approvals does not see the note", async ({ page }) => {
  // Stub zero pending approvals.
  await stubSetApprovals(page, 0);
  await signInSalesManager(page);
  // The note must not be present.
  const quoteDeskTile = page.getByRole("button", { name: "Quote Desk", exact: true });
  await expect(quoteDeskTile).toBeVisible();
  await expect(quoteDeskTile).not.toContainText("approvals waiting");
});

test("the Sales role does not see the note regardless of approval count", async ({ page }) => {
  // Stub a non-zero count, but the Sales role does not have the approvals endpoint.
  await stubSetApprovals(page, 5);
  await signInSales(page);
  // The note must not be present on the Sales home.
  const quoteDeskTile = page.getByRole("button", { name: "Quote Desk", exact: true });
  await expect(quoteDeskTile).toBeVisible();
  await expect(quoteDeskTile).not.toContainText("approvals waiting");
});

test("the count reads the contract's page object and follows its cursor", async ({ page }) => {
  await stubSetApprovals(page, 3, {});
  await signInSalesManager(page);
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toContainText("3 approvals waiting");
});

test("the count is the pending margin kind only, the Quote Desk's own number", async ({ page }) => {
  await stubSetApprovals(page, 2, { extra: 4 });
  await signInSalesManager(page);
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toContainText("2 approvals waiting");
});

test("the count is the same for the page object with other kinds mixed in", async ({ page }) => {
  await stubSetApprovals(page, 2, { extra: 3 });
  await signInSalesManager(page);
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toContainText("2 approvals waiting");
});

test("a late approvals answer after the role changed is dropped", async ({ page }) => {
  await stubSetApprovals(page, 3, { delayMs: 1500 });
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
  // Switch role while the approvals request is still out, then sign in as Sales.
  await page.getByRole("button", { name: /switch role/i }).click();
  await signInSales(page);
  // Let the late answer land, then check the Sales home never shows the note.
  await page.waitForTimeout(2500);
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).not.toContainText("approvals waiting");
});
