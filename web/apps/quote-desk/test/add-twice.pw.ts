// Round 10 item 22 (row 1092): the same product added twice raises the quantity of the line it is already on, and
// says so in words, unless the lines differ by price (a discount) or note. The empty ticket says "Search", not "Scan".
import { expect, test, type Page } from "@playwright/test";
import { writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function newQuote(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
}

const scan = (page: Page) => page.getByRole("combobox", { name: "Search products by name, code or category" });

async function addByCode(page: Page, code: string): Promise<void> {
  await scan(page).fill(code);
  await scan(page).press("Enter");
}

test("an empty ticket says to search, in the words of the box above it", async ({ page }) => {
  await newQuote(page);
  await expect(page.getByTestId("qb-line")).toHaveCount(0);
  const empty = page.locator("qd-quote-workarea").getByText(/No lines yet/);
  await expect(empty).toContainText("Search for a product");
  await expect(empty).not.toContainText("Scan");
});

test("adding a product that is already on the quote raises its quantity and says so", async ({ page }) => {
  await newQuote(page);
  await addByCode(page, "PSN-100");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const seen = writes(page);
  await page.waitForTimeout(500);
  await addByCode(page, "PSN-100");
  await expect(page.getByText(/already on the quote/)).toBeVisible();
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-line").first().locator('[data-field="qty"]')).toHaveValue("2");
  expect(seen.filter((w) => /POST .*\/lines /.test(w))).toEqual([]);
  expect(seen.filter((w) => /PATCH .*\/lines\//.test(w))).toHaveLength(1);
});

test("a line that carries a discount is a different price, so the same product is a second line", async ({ page }) => {
  await newQuote(page);
  await addByCode(page, "PSN-100");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const disc = page.getByTestId("qb-line").first().locator('[data-field="disc"]');
  await disc.fill("5");
  await disc.blur();
  await expect(disc).toHaveValue(/^5/);
  await page.waitForTimeout(500);
  await addByCode(page, "PSN-100");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
});
