// The Quote Desk's list and shell in a real browser, against the stubbed
// harness: the status filter reaches the server (the rows that come back are
// the rows the server filtered), opening a row mounts it in the work area,
// the create form makes a new quote through the picker, and the detail
// disclosure hosts the named seams the second and third coders fill. The
// stub's world is in memory, so the tests run in order.
import { expect, test, type Page } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("the list answers the caller's own quotes and the status filter reaches the server", async ({ page }) => {
  await signIn(page);
  // The seeded world: two quotes the Sales login owns (Draft and Accepted),
  // and one Sent quote the manager owns. A Work caller's list defaults to
  // its own: the manager's quote is not in it.
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(2);
  await expect(page.getByTestId("qb-list")).toContainText("Draft");
  await expect(page.getByTestId("qb-list")).toContainText("Accepted");
  await expect(page.getByTestId("qb-list")).not.toContainText("Fenwick");

  // The filter: Draft alone answers one row.
  await page.getByTestId("qb-list-filter").selectOption("DRAFT");
  await expect(rows).toHaveCount(1);
  await expect(page.getByTestId("qb-list")).toContainText("Draft");

  // Sent answers none for this caller (the only Sent quote is the manager's).
  await page.getByTestId("qb-list-filter").selectOption("SENT");
  await expect(page.getByTestId("qb-list")).toContainText("No quotes.");

  // All statuses brings the caller's own two back.
  await page.getByTestId("qb-list-filter").selectOption("");
  await expect(rows).toHaveCount(2);
});

test("the Sales manager's list reaches every quote in its branch", async ({ page }) => {
  await signIn(page, "Sales manager");
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(3);
  await expect(page.getByTestId("qb-list")).toContainText("Fenwick");
});

test("opening a row mounts it in the work area", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Accepted" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  // The header's context names the open quote's number.
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  // The accepted quote offers the conversion and no line edit.
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  await expect(page.getByTestId("qb-line-qty")).toHaveCount(0);
});

test("the create form makes a quote and opens it", async ({ page }) => {
  await signIn(page);
  await page.getByRole("group", { name: "New quote" }).locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Fenwick");
  await page.getByRole("group", { name: "New quote" }).locator("hos-customer-picker").getByRole("option", { name: /Fenwick Builders/ }).click();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  await expect(page.getByTestId("qb-identity")).toContainText("Fenwick Builders");
});

test("the detail disclosure hosts the named seams and reaches them on the fan-out", async ({ page }) => {
  await signIn(page);
  // The seams are mounted for an open quote only: open the Draft row first.
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByText("Quote detail & admin").click();
  await expect(page.locator('[data-seam="edit-panel"]')).toBeAttached();
  await expect(page.locator('[data-seam="history"]')).toBeAttached();
  await expect(page.locator('[data-seam="settings"]')).toBeAttached();
  await expect(page.locator('[data-seam="assist"]')).toBeAttached();
  await expect(page.locator('[data-seam="followups"]')).toBeAttached();
  await expect(page.locator('[data-seam="hold-expiry"]')).toBeAttached();
  await expect(page.locator('[data-seam="availability"]')).toBeAttached();
  await expect(page.locator('[data-seam="approvals"]')).toBeAttached();
});
