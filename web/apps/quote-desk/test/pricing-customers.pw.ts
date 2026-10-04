// The customer prices tab in a real browser, against the stubbed harness: pick a customer,
// see their category prices (or "system based"), add, edit and delete one, the floor
// warning on a deep price, and the guard that an answer for the customer the picker has
// left never lands under the new one.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, pricingState, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const WALLS = "ca700000-0000-4000-9000-000000000002";

const picker = (page: Page) => page.locator("qd-pricing-customers hos-customer-picker");

async function pick(page: Page, search: string, name: RegExp): Promise<void> {
  await picker(page).getByPlaceholder("Search name or account #…").fill(search);
  await picker(page).getByRole("option", { name }).click();
}

async function openCustomers(page: Page): Promise<void> {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "customers");
}

test("before a customer is picked the tab says how it works and offers no Add", async ({ page }) => {
  await openCustomers(page);
  await expect(page.getByTestId("pc-hint")).toContainText("system based");
  await expect(page.getByTestId("pc-add")).toHaveCount(0);
  await expect(page.getByTestId("pc-count")).toHaveText("search a customer");
});

test("a customer with no prices is system based, and Add appears", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await expect(page.getByTestId("pc-system")).toContainText("System based");
  await expect(page.getByTestId("pc-count")).toHaveText("Maple Ridge Contracting · 0 prices");
  await expect(page.getByTestId("pc-add")).toBeVisible();
});

test("a gentle price saves straight away and lists, then edits and deletes", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(WALLS);
  await page.getByTestId("pc-value").fill("5");
  await page.getByTestId("pc-notes").fill("Quarterly agreement");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pc-row")).toHaveCount(1);
  await expect(page.getByTestId("pc-row")).toContainText("Retaining Walls");
  await expect(page.getByTestId("pc-row")).toContainText("5.00% off");
  await expect(page.getByTestId("pc-row")).toContainText("Quarterly agreement");
  await shot(page.locator("qd-pricing-customers"), "pricing-customer-prices");

  await page.getByTestId("pc-edit").click();
  await expect(page.getByTestId("pc-category")).toBeDisabled();
  await expect(page.getByTestId("pc-value")).toHaveValue("5.00");
  await page.getByTestId("pc-value").fill("6.5");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-row")).toContainText("6.50% off");

  await page.getByTestId("pc-delete").click();
  await expect(page.getByTestId("pc-confirm")).toContainText("goes back to system pricing");
  await page.getByTestId("pc-delete-confirm").click();
  await expect(page.getByTestId("pc-system")).toBeVisible();
  expect((await pricingState(page)).overrides).toHaveLength(0);
});

test("a deep price warns, declining saves nothing, confirming saves and logs against the customer", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(PAVERS);
  await page.getByTestId("pc-value").fill("22");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("floor-headline")).toHaveText("This price puts 2 products under their margin floor.");
  await page.getByTestId("floor-decline").click();
  expect((await pricingState(page)).overrides).toHaveLength(0);
  expect((await pricingState(page)).confirmations).toHaveLength(0);

  await page.getByTestId("pc-save").click();
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("pc-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pc-row")).toContainText("22.00% off");
  const state = await pricingState(page);
  expect(state.overrides).toHaveLength(1);
  expect(state.confirmations[0]).toMatchObject({ entity_type: "customer_override", entity_id: state.overrides[0]?.id, ref_entity_type: "customer", ref_entity_id: "c-maple-ridge" });
});

test("the pricing log links a customer row back to that customer's prices", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(WALLS);
  await page.getByTestId("pc-value").fill("5");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-row")).toHaveCount(1);
  await openTab(page, "audit");
  await page.getByTestId("pa-link").filter({ hasText: "Open customer pricing" }).first().click();
  await expect(page.getByTestId("pricing-tab-customers")).toHaveAttribute("aria-current", "true");
  await expect(page.getByTestId("pc-row")).toHaveCount(1);
  await expect(page.getByTestId("pc-row")).toContainText("Retaining Walls");
});

test("a save the server refuses is shown in the dialog, never silently", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(WALLS);
  await page.getByTestId("pc-value").fill("5");
  await page.request.put("/_stub/pricing-mode", { data: { fail_next: { kind: "save", status: 409, message: "pricing: an override already exists for this customer + category" } } });
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-form-error")).toHaveText("An override already exists for this customer + category");
  await expect(page.getByTestId("pc-dialog")).toBeVisible();
  expect((await pricingState(page)).overrides).toHaveLength(0);
});

test("a late answer for the customer the picker has left never lands under the new one", async ({ page }) => {
  await openCustomers(page);
  // Maple has a price; Fenwick has none. Maple's list is held, Fenwick is picked and answers first.
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(WALLS);
  await page.getByTestId("pc-value").fill("5");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-row")).toHaveCount(1);

  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let held = false;
  await page.route("**/api/v1/pricing/customers/c-maple-ridge/overrides*", async (route) => {
    if (!held) {
      held = true;
      await gate;
    }
    await route.continue();
  });
  // Re-pick Maple (the held read), then change to Fenwick before it answers.
  await page.getByRole("button", { name: /Change customer/ }).click();
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await expect.poll(() => held).toBe(true);
  await page.getByRole("button", { name: /Change customer/ }).click();
  await pick(page, "Fenwick", /Fenwick Builders/);
  await expect(page.getByTestId("pc-system")).toBeVisible();
  await expect(page.getByTestId("pc-count")).toHaveText("Fenwick Builders Ltd · 0 prices");
  release();
  // The held Maple answer arrives now and must be dropped.
  await page.waitForResponse("**/api/v1/pricing/customers/c-maple-ridge/overrides*");
  await expect(page.getByTestId("pc-system")).toBeVisible();
  await expect(page.getByTestId("pc-row")).toHaveCount(0);
  await expect(page.getByTestId("pc-count")).toHaveText("Fenwick Builders Ltd · 0 prices");
});

test("a save is bound to the customer it began for, even if the picker moves while the warning is open", async ({ page }) => {
  await openCustomers(page);
  await pick(page, "Maple", /Maple Ridge Contracting/);
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-category").selectOption(PAVERS);
  await page.getByTestId("pc-value").fill("22");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  // Nothing can move the picker under a modal; the confirm writes for the customer captured at the start.
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("pc-dialog")).toHaveCount(0);
  const state = await pricingState(page);
  expect(state.overrides[0]).toMatchObject({ customer_id: "c-maple-ridge", category_id: PAVERS });
});
