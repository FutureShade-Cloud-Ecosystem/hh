// Round 11 item 6: clicking a customer to start a New quote no longer leaves a Draft behind. The quote is made on the
// first line the person adds; until then nothing is on the server, and Change goes back to the picker with nothing made.
import { expect, test, type Page } from "@playwright/test";
import { showList, writes } from "./switch-helpers.js";

/** The creates made so far: POST /api/v1/quotes itself (the shared writes() list names only the quote paths below it). */
function creates(page: Page): string[] {
  const made: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/quotes") made.push(r.url());
  });
  return made;
}

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function pick(page: Page, name = "Maple"): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill(name);
  await picker.getByRole("option", { name: new RegExp(name === "Maple" ? "Maple Ridge Contracting" : name) }).click();
}

async function listCount(page: Page): Promise<number> {
  await showList(page);
  return page.getByTestId("qb-list-row").count();
}

test("picking a customer makes no quote, and adding the first line makes exactly one", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("qb-list-row").first()).toBeVisible();
  const before = await page.getByTestId("qb-list-row").count();
  const seen = writes(page);
  const made = creates(page);
  await pick(page);
  await expect(page.getByTestId("qb-compose-customer")).toContainText("Maple Ridge Contracting");
  await expect(page.getByRole("combobox", { name: "Search products by name, code or category" })).toBeFocused();
  await page.waitForTimeout(500);
  expect(made).toEqual([]);
  // Leaving leaves nothing behind.
  expect(await listCount(page)).toBe(before);
  // Coming back and adding a line makes the one quote, with the line on it.
  await pick(page);
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  expect(made).toHaveLength(1);
  expect(seen.filter((w) => /POST .*\/lines /.test(w))).toHaveLength(1);
  expect(await listCount(page)).toBe(before + 1);
});

test("Change goes back to the picker with nothing made", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const seen = writes(page);
  const made = creates(page);
  await pick(page);
  await page.getByTestId("qb-compose-change").click();
  await expect(page.getByTestId("qb-identity").locator("hos-customer-picker")).toBeVisible();
  expect(made).toEqual([]);
});
