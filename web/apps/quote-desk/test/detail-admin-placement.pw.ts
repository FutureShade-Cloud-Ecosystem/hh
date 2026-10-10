// The "Quote detail & admin" disclosure's placement, as the plan decides it
// (QD-0-plan-r1.md), under the split-list navigation (decision log row 941):
// the disclosure lives on the QUOTE page, under the work area, never under
// the list. The demo polish round asked for it to be hidden from the Sales
// and Sales manager desks UNLESS the plan places it there, and to say which.
// The plan places it on both demo desks, and builds no third login:
//
//   row 2 hosts the edit panel, the history, the assist panel and the related
//   documents on the desk's shell; row 3 ports the edit panel (the per line
//   Save, Remove, Compare table) as a desk screen whose controls are the
//   SERVER's offers (editable_fields), never the app's rule (section 3.5).
//   QD-1 carries the requester's view of a margin request's state, which is
//   the approvals panel: under the split-list navigation that panel stands on
//   the list page's approvals view (the Sales manager's Approvals tab, and
//   the disclosure's own Approvals way in for the requester). The settings
//   entry is offered to everyone and the server answers who may write.
//   QD-6 builds the way in for exactly two logins, Sales and Sales manager:
//   there is no Administrator desk to keep it for, and the demo's
//   administrator-facing surface is the Sales manager's (the settings
//   screen's own profile word for that role is admin).
//
// So the hide set is empty: this test pins the placement and the role gate
// that IS the plan's, the server's own offers inside the panels.
import { expect, test, type Page } from "@playwright/test";
import { showList } from "./switch-helpers.js";

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
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  // The disclosure is on the quote's own page now, never under the list.
  await expect(page.getByTestId("qd-list-toolbar")).toBeHidden();
}

test("the disclosure stands on the Sales desk's quote page, with the panels mounted for the late answers", async ({ page }) => {
  await openQuoteAs(page, "Sales");
  const detail = page.getByTestId("qb-detail");
  await expect(detail).toBeAttached();
  await expect(page.getByText("Quote detail & admin")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qb-quote-panels")).toBeVisible();
  await expect(page.locator("qd-quote-edit")).toBeAttached();
  // The role gate that holds is the server's own: the Sales caller is offered
  // no decide control on an approval row (the requester's view, QD-1), and
  // the requester's way to that view is the disclosure's Approvals way in.
  await page.getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.locator("qd-approvals")).toBeVisible();
  await expect(page.getByTestId("qb-list")).toBeHidden();
});

test("the disclosure stands on the Sales manager desk's quote page, the demo's administrator-facing surface", async ({ page }) => {
  await openQuoteAs(page, "Sales manager");
  const detail = page.getByTestId("qb-detail");
  await expect(detail).toBeAttached();
  await expect(page.getByText("Quote detail & admin")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qb-quote-panels")).toBeVisible();
  await expect(page.locator("qd-quote-edit")).toBeAttached();
});
