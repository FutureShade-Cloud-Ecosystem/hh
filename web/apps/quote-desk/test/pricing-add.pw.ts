// QD-PRICING-ADD (decision log row 988): the manager can add a category's margin policy, and
// the other pricing tabs are audited for their create, edit and retire actions. The server's
// PUT creates or replaces one category's policy, so the Margin policy tab ends in an Add
// category row; the API has no remove route for a policy, so no Remove is drawn. The rules,
// customer prices and quantity breaks tabs already carry every action the API offers (the
// audit legs pin that), and the pricing log is read only. Sales sees none of it.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, pricingState, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";
const WALLS = "ca700000-0000-4000-9000-000000000002";
const SLABS = "ca700000-0000-4000-9000-000000000003";
const AGGREGATES = "ca700000-0000-4000-9000-000000000004";
const ACCESSORIES = "ca700000-0000-4000-9000-000000000005";
const BULK = "ca700000-0000-4000-9000-000000000006";

test.use({ viewport: { width: 1440, height: 900 } });

async function setLight(page: Page): Promise<void> {
  const control = page.getByTestId("theme-control");
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes("Light")) return;
    await control.click();
  }
  await expect(control).toContainText("Light");
}

/**
 * The live demo's state: only Pavers and Bulk hold a margin policy. The contract has no
 * categories read, so the picker offers the categories the pricing reads prove exist (a rule
 * or a quantity break scoped to one): here a tier rule names each of the four.
 */
async function twoPolicies(page: Page): Promise<void> {
  await setMode(page, { drop_policies: [WALLS, SLABS, AGGREGATES, ACCESSORIES], rule_categories: [WALLS, SLABS, AGGREGATES, ACCESSORIES] });
  await signIn(page);
  await setLight(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
}

test("A: the manager sees an Add category row under Pavers and Bulk", async ({ page }) => {
  await twoPolicies(page);
  await expect(page.getByTestId("pm-add-row")).toBeVisible();
  const picker = page.getByTestId("pm-add-category");
  // The picker lists exactly the categories that have no policy yet.
  await expect(picker.locator("option:not([value=''])")).toHaveText(["Retaining Walls", "Slabs & Steps", "Aggregates", "Accessories"]);
  await expect(page.getByTestId("pm-add-target")).toBeVisible();
  await expect(page.getByTestId("pm-add-floor")).toBeVisible();
  await expect(page.getByTestId("pm-add-enforce")).toBeChecked();
  await expect(page.getByTestId("pm-add-save")).toBeVisible();
  await shot(page, `${process.env.QD_SHOT_PREFIX ?? "after"}-margin-policy-add-manager-desktop`);
});

test("A: the picker opened shows the un-covered categories", async ({ page }) => {
  await twoPolicies(page);
  await page.getByTestId("pm-add-category").focus();
  await shot(page.getByTestId("pm-panel"), "after-margin-policy-add-picker-open-manager-desktop");
});

test("A: Save sends the PUT and the new row joins the grid, editable in place", async ({ page }) => {
  await twoPolicies(page);
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("31.5");
  await page.getByTestId("pm-add-floor").fill("20");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-row")).toHaveCount(3);
  const row = page.locator('tr[data-category-id="' + WALLS + '"]');
  await expect(row.getByTestId("pm-category")).toHaveText("Retaining Walls");
  await expect(row.locator('input[data-field="target-' + WALLS + '"]')).toHaveValue("31.50");
  await expect(row.locator('input[data-field="floor-' + WALLS + '"]')).toHaveValue("20.00");
  await expect(row.locator('input[type="checkbox"]')).toBeChecked();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  const state = await pricingState(page);
  expect(state.policies.find((p) => p.category_id === WALLS)).toMatchObject({ margin_target_bps: 3150, margin_floor_bps: 2000, margin_enforcement_disabled: false });
  expect(state.audit.some((r) => r.entity_type === "category_policy" && r.entity_id === WALLS && r.action === "CREATE")).toBe(true);
  // The category left the picker, and the row edits and saves in place like the others.
  await expect(page.getByTestId("pm-add-category").locator("option:not([value=''])")).toHaveText(["Slabs & Steps", "Aggregates", "Accessories"]);
  await row.locator('input[data-field="target-' + WALLS + '"]').fill("33");
  await expect(row.getByTestId("pm-save")).toHaveClass(/primary/);
  await row.getByTestId("pm-save").click();
  await expect(page.getByTestId("pm-saved")).toBeVisible();
  expect((await pricingState(page)).policies.find((p) => p.category_id === WALLS)).toMatchObject({ margin_target_bps: 3300, margin_floor_bps: 2000 });
});

test("A: the Enforce box unticked saves the category with enforcement off", async ({ page }) => {
  await twoPolicies(page);
  await page.getByTestId("pm-add-category").selectOption(ACCESSORIES);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("20");
  await page.getByTestId("pm-add-enforce").uncheck();
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-row")).toHaveCount(3);
  expect((await pricingState(page)).policies.find((p) => p.category_id === ACCESSORIES)).toMatchObject({ margin_enforcement_disabled: true });
});

test("A: no category, a bad number or a floor over the target is said in the row and nothing is sent", async ({ page }) => {
  await twoPolicies(page);
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-add-error")).toHaveText("Pick a category to add.");
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("40");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-add-error")).toHaveText("The floor must be at or below the target.");
  await page.getByTestId("pm-add-target").fill("abc");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-add-error")).toBeVisible();
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(0);
  expect(state.previews).toHaveLength(0);
});

test("A: the picker offers only categories the pricing reads prove exist, never a built-in list", async ({ page }) => {
  // Pavers and Bulk hold a policy; the only other category any rule or break names is Accessories.
  await setMode(page, { drop_policies: [WALLS, SLABS, AGGREGATES, ACCESSORIES] });
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
  await expect(page.getByTestId("pm-add-category").locator("option:not([value=''])")).toHaveText(["Accessories"]);
});

test("A: a category dropped from the grid is offered back because a rule and a break name it", async ({ page }) => {
  await setMode(page, { drop_policies: [WALLS, SLABS, AGGREGATES, ACCESSORIES, PAVERS] });
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(1);
  await expect(page.getByTestId("pm-add-category").locator("option:not([value=''])")).toHaveText(["Pavers", "Accessories"]);
});

test("A: a category the server no longer holds is refused in its words, and nothing is added", async ({ page }) => {
  await twoPolicies(page);
  // The catalogue loses the category between the read and the save: the server's foreign key refusal.
  await setMode(page, { catalogue: [PAVERS, BULK] });
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("20");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-add-error")).toHaveText("References a nonexistent customer, product, category or branch");
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
  expect((await pricingState(page)).policies.find((p) => p.category_id === WALLS)).toBeUndefined();
});

test("A: a target or a floor above 100 or below 0 is refused in plain words and nothing is sent", async ({ page }) => {
  await twoPolicies(page);
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  for (const [target, floor] of [["150", "20"], ["30", "100.5"], ["-5", "-10"], ["30", "-1"]]) {
    await page.getByTestId("pm-add-target").fill(target);
    await page.getByTestId("pm-add-floor").fill(floor);
    await page.getByTestId("pm-add-save").click();
    await expect(page.getByTestId("pm-add-error")).toHaveText("Target and floor must be between 0% and 100%.");
  }
  // The edge values themselves are accepted.
  await page.getByTestId("pm-add-target").fill("100");
  await page.getByTestId("pm-add-floor").fill("0");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-row")).toHaveCount(3);
});

test("A: a row's own Save refuses a target over 100 before sending", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  const row = page.locator('tr[data-category-id="' + PAVERS + '"]');
  await row.locator('input[data-field="target-' + PAVERS + '"]').fill("150");
  await row.getByTestId("pm-save").click();
  await expect(row.getByTestId("pm-error")).toHaveText("Target and floor must be between 0% and 100%.");
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(0);
  expect(state.previews).toHaveLength(0);
});

test("A: a floor that puts products under it asks first, and declining adds nothing", async ({ page }) => {
  await setMode(page, { drop_policies: [PAVERS] });
  await signIn(page);
  await openPricing(page);
  await page.getByTestId("pm-add-category").selectOption(PAVERS);
  await page.getByTestId("pm-add-target").fill("45");
  await page.getByTestId("pm-add-floor").fill("40");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await page.getByTestId("floor-decline").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pm-row")).toHaveCount(5);
  expect((await pricingState(page)).writes).toHaveLength(0);
});

test("A: a refused add is shown in the row, never silently", async ({ page }) => {
  await twoPolicies(page);
  await setMode(page, { fail_next: { kind: "save", status: 422, message: "margin must be between 0% and 100%" } });
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("20");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pm-add-error")).toBeVisible();
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
});

test("A: with every category covered the add row says so and offers no picker", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.getByTestId("pm-add-none")).toHaveText("Every category has a margin policy.");
  await expect(page.getByTestId("pm-add-category")).toHaveCount(0);
  await expect(page.getByTestId("pm-add-save")).toHaveCount(0);
});

test("A: no Remove is drawn on any margin row, because the API has no route to remove a policy", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  await expect(page.locator("qd-pricing-margin").getByRole("button", { name: /remove|delete|retire/i })).toHaveCount(0);
});

test("B: the manager has Rules New, Edit and Retire (the API's create, sparse edit and soft retire)", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-row")).toHaveCount(5);
  await expect(page.getByTestId("pr-new")).toBeVisible();
  await expect(page.getByTestId("pr-edit")).toHaveCount(5);
  await expect(page.getByTestId("pr-retire")).toHaveCount(5);
  await shot(page, "after-rules-tab-manager-desktop");
});

test("C: the manager has Customer prices Add, Edit and Delete (the API's create, update and delete)", async ({ page }) => {
  await signIn(page);
  await setLight(page);
  await openPricing(page);
  await openTab(page, "customers");
  const picker = page.locator("qd-pricing-customers hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("pc-add")).toBeVisible();
  await page.getByTestId("pc-add").click();
  await page.getByTestId("pc-value").fill("6");
  await page.getByTestId("pc-save").click();
  await expect(page.getByTestId("pc-edit")).toHaveCount(1);
  await expect(page.getByTestId("pc-delete")).toHaveCount(1);
  await shot(page, "after-customer-prices-tab-manager-desktop");
});

test("D: the manager has Quantity breaks New, Edit and Retire (the API's create, replace and soft retire)", async ({ page }) => {
  await signIn(page);
  await setLight(page);
  await openPricing(page);
  await openTab(page, "breaks");
  await expect(page.getByTestId("pb-new")).toBeVisible();
  await expect(page.getByTestId("pb-edit").first()).toBeVisible();
  await expect(page.getByTestId("pb-retire").first()).toBeVisible();
  await shot(page, "after-quantity-breaks-tab-manager-desktop");
});

test("E: the pricing log is read only, with no Save, Add, New, Edit, Remove or Retire", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-error")).toHaveCount(0);
  await expect(page.locator("qd-pricing-audit").getByRole("button", { name: /save|add|new|edit|remove|delete|retire/i })).toHaveCount(0);
  await expect(page.locator("qd-pricing-audit input[type=text], qd-pricing-audit textarea")).toHaveCount(0);
});

test("F: Sales has no Pricing tab and none of the Add, Edit, Remove, Save or Retire controls", async ({ page }) => {
  // The reconciliation put Pricing on a manager desk tab under the shared header: Sales is not given the tab at all.
  await signIn(page, "Sales");
  await expect(page.getByTestId("desk-tab-pricing")).toHaveCount(0);
  await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
  for (const id of ["pm-add-row", "pm-add-category", "pm-add-save", "pm-save", "pr-new", "pr-edit", "pr-retire", "pc-add", "pc-edit", "pc-delete", "pb-new", "pb-edit", "pb-retire"]) {
    await expect(page.getByTestId(id)).toHaveCount(0);
  }
  await expect(page.locator("qd-pricing-panel button[data-action='save']")).toHaveCount(0);
  expect((await pricingState(page)).writes).toHaveLength(0);
});

test("F: a login the server lets read but not write loses the Add row with the other controls", async ({ page }) => {
  await twoPolicies(page);
  await setMode(page, { mode: "readonly" });
  await page.getByTestId("pm-add-category").selectOption(WALLS);
  await page.getByTestId("pm-add-target").fill("30");
  await page.getByTestId("pm-add-floor").fill("20");
  await page.getByTestId("pm-add-save").click();
  await expect(page.getByTestId("pricing-readonly")).toBeVisible();
  await expect(page.getByTestId("pm-add-row")).toHaveCount(0);
  await expect(page.getByTestId("pm-add-save")).toHaveCount(0);
  await expect(page.getByTestId("pm-row")).toHaveCount(2);
});
