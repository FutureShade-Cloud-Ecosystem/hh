// The quotes list and the New quote form under the desk carry the design
// system's controls, not raw browser chrome: the bare select, the native
// date input, the plain Refresh and Create buttons and the fieldset legend
// are the demo round's defect 2. The card scoped here is the one that holds
// the list's status filter (the list box), which holds both the filter row
// and the New quote group.
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

/** The list box: the card that holds the status filter, the list and the New quote group. */
async function quotesCard(page: Page): Promise<ReturnType<Page["locator"]>> {
  const card = page.locator("qd-quote-builder .box").filter({ has: page.getByTestId("qb-list-filter") });
  await expect(card).toBeVisible();
  return card;
}

test("the quotes card holds no bare native controls", async ({ page }) => {
  await signIn(page);
  const card = await quotesCard(page);
  await expect(card.locator("select:not([data-control])")).toHaveCount(0);
  await expect(card.locator('input[type="date"]:not([data-control])')).toHaveCount(0);
  // The control rows (the filter row and the New quote group's row) hold no
  // plain button of their own: the list's row buttons are the card's content
  // and the picker's own control rides inside the kit, so the leg reads the
  // rows' direct children.
  await expect(card.locator(".row > button:not(.btn-primary):not(.btn-secondary)")).toHaveCount(0);
  await expect(card.locator("fieldset:not([data-control])")).toHaveCount(0);
});

test("the quotes card's controls carry the design system's control classes", async ({ page }) => {
  await signIn(page);
  const card = await quotesCard(page);
  const filter = card.locator('select[data-control="select"][data-field="status-filter"]');
  await expect(filter).toBeVisible();
  await expect(filter).toContainText("All statuses");
  const refresh = card.locator('button.btn-secondary[data-action="refresh"]');
  await expect(refresh).toBeVisible();
  await expect(refresh).toHaveText("Refresh");
  const branch = card.locator('select[data-control="select"][data-field="new-branch"]');
  await expect(branch).toBeVisible();
  const expiry = card.locator('input[type="date"][data-control="date"][data-field="new-expiry"]');
  await expect(expiry).toBeVisible();
  const create = card.locator('button.btn-primary[data-action="create"]');
  await expect(create).toBeVisible();
  await expect(create).toHaveText("Create");
});

test("the new quote group's heading carries the legend's words", async ({ page }) => {
  await signIn(page);
  const card = await quotesCard(page);
  const group = card.locator('fieldset[data-control="field-group"]');
  await expect(group).toBeVisible();
  const heading = group.locator(".field-group-heading");
  await expect(heading).toBeVisible();
  await expect(heading).toHaveText("New quote");
  // The visible heading is also the group's accessible name.
  await expect(page.getByRole("group", { name: "New quote" })).toBeVisible();
});
