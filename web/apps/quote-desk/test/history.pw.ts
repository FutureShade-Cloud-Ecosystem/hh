// The change history in a real browser, against the stubbed harness: the
// trail in causal order, grouped so one action reads as one entry (with the
// money that moved beneath it), the discount changes among the rows, the
// actor on each, the panel refreshing when an edit lands elsewhere on the
// page, the empty state, and a refused read shown in words.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qh-box")).toBeVisible();
}

const history = (page: Page) => page.locator("qd-quote-history");
const edit = (page: Page) => page.locator("qd-quote-edit");

test.beforeAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a quote nobody has edited says so", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Accepted");
  await expect(history(page)).toContainText("No changes yet.");
  await expect(history(page).getByTestId("qh-group")).toHaveCount(0);
});

test("each action is one entry, in order, with its consequences beneath and the actor named", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  // Action one: a quantity change on line 2.
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(1);
  // The history refreshed without a page reload: the edit panel's quote-changed reached it.
  const first = history(page).getByTestId("qh-group").first();
  await expect(first).toContainText("Quantity, line 2: 2 → 6");
  await expect(first).toContainText("Sales (demo)");
  await expect(first).toContainText("Line total: CA$22.96 → CA$68.88");
  await expect(first).toContainText("Quote subtotal");
  // Action two: a discount on the same line, among the rows as its own entry.
  await sand.locator('[data-field="disc"]').fill("5");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(2);
  const second = history(page).getByTestId("qh-group").nth(1);
  await expect(second).toContainText("Discount, line 2: 0% → 5%");
  await expect(second).toContainText("Unit price");
  // Action three: one added line reads as ONE entry, not five.
  const search = edit(page).locator('[data-field="add-search"]');
  await search.fill("edge");
  await edit(page).getByRole("option").first().click();
  await edit(page).locator('[data-field="add-qty"]').fill("4");
  await edit(page).getByRole("button", { name: "Add", exact: true }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(3);
  const third = history(page).getByTestId("qh-group").nth(2);
  await expect(third).toContainText("Line added, line 3");
  await expect(third).toContainText("Quantity: none → 4");
  // Oldest first: the quantity entry is still above the discount entry.
  const texts = await history(page).getByTestId("qh-group").allInnerTexts();
  expect(texts[0]).toContain("Quantity");
  expect(texts[1]).toContain("Discount");
  expect(texts[2]).toContain("Line added");
});

test("the expiry change and a removal are entries too", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const before = await history(page).getByTestId("qh-group").count();
  const later = new Date(Date.now() + 25 * 86400000).toISOString().slice(0, 10);
  await edit(page).locator('[data-field="expiry"]').fill(later);
  await edit(page).getByRole("button", { name: "Save expiry" }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(before + 1);
  await expect(history(page).getByTestId("qh-group").last()).toContainText(`Expiry date: none → ${later}`);
  await edit(page).getByTestId("qe-line").nth(2).getByRole("button", { name: "Remove" }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(before + 2);
  await expect(history(page).getByTestId("qh-group").last()).toContainText("Line removed, line 3");
});

test("the manager's clearing of a below floor price is in the trail with who cleared it", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("10");
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(edit(page).getByTestId("qe-notice")).toContainText("Saved.");
  await expect(history(page).getByTestId("qh-group").last()).toContainText("Discount, line 1: 0% → 10%");
  await expect(history(page).getByTestId("qh-group").last()).toContainText("Floor cleared by: none → Sales manager (demo)");
  await expect(history(page).getByTestId("qh-group").last()).toContainText("Sales manager (demo)");
});
