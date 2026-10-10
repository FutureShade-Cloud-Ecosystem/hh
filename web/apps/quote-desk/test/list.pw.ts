// The Quote Desk's list and shell in a real browser, against the stubbed
// harness: the status tabs reach the server (the rows that come back are
// the rows the server filtered), opening a row mounts it in the work area,
// the create form makes a new quote through the picker, and the detail
// disclosure hosts the named seams the second and third coders fill. The
// stub's world is in memory, so the tests run in order.
import { expect, test, type Page } from "@playwright/test";
import { showList } from "./switch-helpers.js";

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

test("the list answers the caller's own quotes and the status tabs reach the server", async ({ page }) => {
  await signIn(page);
  // The seeded world: four quotes the Sales login owns (Draft, Accepted,
  // Accepted of the cash customer, Converted), and one Sent quote the manager
  // owns. A Work caller's list defaults to its own: the manager's quote is
  // not in it.
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(4);
  await expect(page.getByTestId("qb-list")).toContainText("Draft");
  await expect(page.getByTestId("qb-list")).toContainText("Accepted");
  await expect(page.getByTestId("qb-list")).toContainText("Converted");
  await expect(page.getByTestId("qb-list")).not.toContainText("Fenwick");

  // The status tabs are the segmented control: Draft alone answers one row.
  const tabs = page.getByTestId("qd-list-tabs");
  await tabs.getByRole("button", { name: "Draft", exact: true }).click();
  await expect(rows).toHaveCount(1);
  await expect(page.getByTestId("qb-list")).toContainText("Draft");

  // Sent answers none for this caller (the only Sent quote is the manager's).
  await tabs.getByRole("button", { name: "Sent", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toContainText("No quotes.");

  // All brings the caller's own four back.
  await tabs.getByRole("button", { name: "All", exact: true }).click();
  await expect(rows).toHaveCount(4);
});

test("the Sales manager's list reaches every quote in its branch", async ({ page }) => {
  await signIn(page, "Sales manager");
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(5);
  await expect(page.getByTestId("qb-list")).toContainText("Fenwick");
});

test("opening a row mounts it in the work area", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).click();
  await expect(page).toHaveURL(/#\/quotes\/[^/]+$/);
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  // The header's context names the open quote's number.
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  // The accepted quote offers the conversion and no line edit.
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  await expect(page.getByTestId("qb-line-qty")).toHaveCount(0);
});

test("the compose page makes a quote and opens it", async ({ page }) => {
  await signIn(page);
  // New quote walks the desk to the compose page: the pick chooses the customer and the first line made makes the quote.
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page).toHaveURL(/#\/quotes\/new$/);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick Builders/ }).click();
  const box = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await box.fill("PSN-100");
  await box.press("Enter");
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  await expect(page.getByTestId("qb-identity")).toContainText("Fenwick Builders");
});

test("the detail disclosure hosts the named seams and reaches them on the fan-out", async ({ page }) => {
  await signIn(page);
  // The seams are mounted for an open quote only: open the Draft row first.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByText("Quote detail & admin").click();
  for (const seam of ["edit-panel", "history", "settings", "assist", "followups", "hold-expiry", "availability"]) {
    await expect(page.locator(`[data-seam="${seam}"]`)).toBeAttached();
  }
  // The approvals seam stands on the list page's approvals view, not in the
  // disclosure (the split-list round): the requester reaches it through the
  // disclosure's own way in.
  await expect(page.locator('[data-testid="qb-detail"] [data-seam="approvals"]')).toHaveCount(0);
  await page.getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.locator('[data-seam="approvals"]')).toBeAttached();
});
