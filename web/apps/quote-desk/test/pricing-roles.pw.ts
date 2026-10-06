// The wrong role proof (plan 3.4, ledger rows 11 and 12; QD-4): the app never decides a
// role rule. The Pricing button is on every signed in desk; the server answers. The Sales
// manager sees the panel. The Inside Sales login, which the server's gate refuses, is shown
// the server's refusal and no controls. A login the server lets read but not write loses its
// controls the moment a write is refused, with the prices still readable. A log the cost and
// margin list refuses is refused on its own tab only.
import { expect, test } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";

test("the Sales manager sees the panel: five tabs, the rows and the controls", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openPricing(page);
  for (const tab of ["margin", "rules", "customers", "breaks", "audit"]) {
    await expect(page.getByTestId(`pricing-tab-${tab}`)).toBeVisible();
  }
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.getByTestId("pm-save").first()).toBeVisible();
  await expect(page.getByTestId("pricing-denied")).toHaveCount(0);
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-new")).toBeVisible();
});

test("a role off the list is shown the server's refusal and no controls", async ({ page }) => {
  await signIn(page, "Sales");
  await expect(page.getByTestId("pricing-toggle")).toBeVisible();
  await openPricing(page);
  await expect(page.getByTestId("pricing-denied")).toBeVisible();
  await expect(page.getByTestId("pricing-denied-words")).toHaveText("The server answered: Forbidden");
  // No tabs, no rows, no Save, no New rule, no inputs: nothing to press.
  await expect(page.getByTestId("pricing-tab-margin")).toHaveCount(0);
  await expect(page.getByTestId("pm-row")).toHaveCount(0);
  await expect(page.getByTestId("pm-save")).toHaveCount(0);
  await expect(page.getByTestId("pr-new")).toHaveCount(0);
  await expect(page.locator("qd-pricing-panel input, qd-pricing-panel select")).toHaveCount(0);
  await shot(page.getByTestId("pricing-panel"), "pricing-refused");
});

test("the refused role has made no pricing write, and the quotes screen still works for it", async ({ page }) => {
  await signIn(page, "Sales");
  await openPricing(page);
  await expect(page.getByTestId("pricing-denied")).toBeVisible();
  const state = await (await page.request.get("/_stub/pricing-state")).json();
  expect(state.writes).toHaveLength(0);
  expect(state.previews).toHaveLength(0);
  await page.getByTestId("pricing-toggle").click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
});

test("a login that may read but not write loses its controls when the server refuses a write", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openPricing(page);
  await setMode(page, { mode: "readonly" });
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pricing-readonly")).toHaveText("The server refused a change from this login: Forbidden. The prices stay readable and the controls are off.");
  await expect(page.getByTestId("pm-save")).toHaveCount(0);
  await expect(page.locator("qd-pricing-margin input")).toHaveCount(0);
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.getByTestId("pm-row").first()).toContainText("33.00%");
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-row")).toHaveCount(5);
  await expect(page.getByTestId("pr-new")).toHaveCount(0);
  await expect(page.getByTestId("pr-edit")).toHaveCount(0);
  await expect(page.getByTestId("pr-retire")).toHaveCount(0);
  await openTab(page, "breaks");
  await expect(page.getByTestId("pb-new")).toHaveCount(0);
});

test("a log the cost and margin list refuses is refused on its own tab only", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openPricing(page);
  await setMode(page, { audit_allowed: false });
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-error")).toContainText("Forbidden");
  await expect(page.getByTestId("pricing-tab-margin")).toBeVisible();
});

test("signing out closes the pricing screen, and the next login starts on the quotes", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openPricing(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
});
