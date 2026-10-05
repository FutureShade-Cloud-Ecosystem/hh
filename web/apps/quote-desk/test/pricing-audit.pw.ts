// The pricing log tab in a real browser, against the stubbed harness: the rows with their
// record links and before and after figures, the filters, the date range, the pager and
// the window note, the confirmations the floor warning logs, and the log's own refusal.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const WALLS = "ca700000-0000-4000-9000-000000000002";

async function pick(page: Page, search: string, name: RegExp): Promise<void> {
  const picker = page.locator("qd-pricing-customers hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill(search);
  await picker.getByRole("option", { name }).click();
}

/** Two edits as the reference's log has: a margin target and a customer price. */
async function makeTwoEdits(page: Page): Promise<void> {
  await page.locator('input[data-field="target-' + PAVERS + '"]').fill("34");
  await page.locator('tr[data-category-id="' + PAVERS + '"]').getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  await openTab(page, "customers");
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(WALLS);
  await page.getByTestId("pc-value").fill("5");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-row")).toHaveCount(1);
}

test("the log shows what changed, who changed it, and links the record", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await makeTwoEdits(page);
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-row")).toHaveCount(2);
  await expect(page.getByTestId("pa-count")).toHaveText("2 entries");
  // the scroller is a keyboard citizen too (round 3, a11y): focusable, named, a region
  const scroller = page.getByTestId("pa-table-scroll");
  await expect(scroller).toHaveAttribute("tabindex", "0");
  await expect(scroller).toHaveAttribute("role", "region");
  await expect(scroller).toHaveAccessibleName(/scrolls sideways/);
  const policy = page.getByTestId("pa-row").filter({ hasText: "Category policy" });
  await expect(policy).toContainText("Pavers");
  await expect(policy).toContainText("Updated");
  await expect(policy).toContainText("margin_target_bps");
  await expect(policy.getByTestId("pa-change")).toHaveText("3300→3400");
  await expect(policy).toContainText("Sales manager (demo)");
  await expect(policy.getByTestId("pa-link")).toHaveText(/Open category policy/);
  const customer = page.getByTestId("pa-row").filter({ hasText: "Customer override" });
  await expect(customer).toContainText("Created");
  await expect(customer.getByTestId("pa-link")).toHaveText(/Open customer pricing/);
  await expect(page.getByTestId("pa-pager")).toHaveText("1-2 of 2 · page 1 of 1");
  await shot(page.locator("qd-pricing-audit"), "pricing-log");
});

test("a row about a product, and a row with no record, are not links", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("CUSTOMER_PRODUCT");
  await page.getByTestId("pr-customer-id").fill("c0000000-0000-4000-9000-000000000001");
  await page.getByTestId("pr-product-id").fill("a7000000-0000-4000-9000-000000000001");
  await page.getByTestId("pr-calc").selectOption("ABSOLUTE");
  await page.getByTestId("pr-price").fill("3.90");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-link")).toHaveCount(0);
  await expect(page.getByTestId("pa-no-link")).toHaveText("product record (not opened from here)");
});

test("the entity and action filters ask the server, and the date range narrows what came back", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await makeTwoEdits(page);
  await openTab(page, "audit");
  await page.getByTestId("pa-entity").selectOption("category_policy");
  await expect(page.getByTestId("pa-row")).toHaveCount(1);
  await page.getByTestId("pa-entity").selectOption("");
  await page.getByTestId("pa-action").selectOption("CREATE");
  await expect(page.getByTestId("pa-row")).toHaveCount(1);
  await expect(page.getByTestId("pa-row")).toContainText("Customer override");
  await page.getByTestId("pa-action").selectOption("");
  await expect(page.getByTestId("pa-row")).toHaveCount(2);

  await page.getByTestId("pa-from").fill("2099-01-01");
  await expect(page.getByTestId("pa-empty")).toBeVisible();
  await expect(page.getByTestId("pa-count")).toHaveText("0 of 2 entries");
  await page.getByRole("button", { name: "Clear dates" }).click();
  await expect(page.getByTestId("pa-row")).toHaveCount(2);
});

test("a long log is paged, and a log that fills its window says what the range cannot see", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await page.request.post("/_stub/pricing-seed-audit", { data: { count: 60 } });
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-row")).toHaveCount(25);
  await expect(page.getByTestId("pa-pager")).toHaveText("1-25 of 60 · page 1 of 3");
  await expect(page.getByTestId("pa-window")).toHaveCount(0);
  await page.getByRole("button", { name: /Next/ }).click();
  await expect(page.getByTestId("pa-pager")).toHaveText("26-50 of 60 · page 2 of 3");
  await page.getByRole("button", { name: /Next/ }).click();
  await expect(page.getByTestId("pa-row")).toHaveCount(10);
  await expect(page.getByRole("button", { name: /Next/ })).toBeDisabled();

  await page.request.post("/_stub/pricing-seed-audit", { data: { count: 150 } });
  await page.getByTestId("pricing-tab-margin").click();
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-window")).toHaveText("Filtering the most recent 200 entries: older entries are outside this window.");
});

test("the list answered as the contract's page object reads the same", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await setMode(page, { shape: "page" });
  await makeTwoEdits(page);
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-row")).toHaveCount(2);
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-row")).toHaveCount(5);
});

test("a log the cost and margin list refuses shows the server's refusal there, and the price tabs are untouched", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await setMode(page, { audit_allowed: false });
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-error")).toHaveText("The pricing log could not be read. Forbidden");
  await expect(page.getByTestId("pricing-denied")).toHaveCount(0);
  await openTab(page, "margin");
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.getByTestId("pm-save").first()).toBeVisible();
});
