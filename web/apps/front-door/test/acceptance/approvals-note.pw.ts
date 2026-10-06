// The front door's approvals note tests: the Quote Desk tile carries a quiet
// "N approvals waiting" note when the sales manager has pending margin requests,
// and the note is absent in all other states.
//
// The note is rendered by app-root.ts for the Quote Desk tile only when the
// signed-in role is sales-manager and the pending approval count is greater than
// zero. The count comes from GET /api/v1/staff/approvals with status PENDING,
// which the demo stub serves.
//
// Red first: these three legs fail on origin/main (where the note is not built)
// and pass on the demo/approvals-note branch.
import { expect, test, type Page } from "@playwright/test";

async function stubSetApprovals(page: Page, count: number): Promise<void> {
  const res = await page.request.post("/_stub/set-approvals", { data: { count } });
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
