// Round 11 item 6: clicking a customer to start a New quote no longer leaves a Draft behind. The quote is made on the
// first line the person adds; until then nothing is on the server, and Change goes back to the picker with nothing made.
import { expect, test, type Page } from "@playwright/test";
import { hold, showList, writes } from "./switch-helpers.js";

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

test("R11 P2-1 back to New quote after the first line is a fresh compose, and the next line makes a second quote only after a fresh pick", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const made = creates(page);
  await pick(page);
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  expect(made).toHaveLength(1);
  await page.goBack();
  await expect(page).toHaveURL(/#\/quotes\/new$/);
  // A fresh compose: the picker, not the customer of the quote already made, and no search box to add a line to.
  await expect(page.getByTestId("qb-compose-customer")).toHaveCount(0);
  await expect(page.getByTestId("qb-identity").locator("hos-customer-picker")).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Search products by name, code or category" })).toHaveCount(0);
  expect(made).toHaveLength(1);
});

test("R11 P2-1 arriving at New quote by the address is a fresh compose too", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await pick(page);
  await expect(page.getByTestId("qb-compose-customer")).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = "#/quotes/q-1001";
  });
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await page.evaluate(() => {
    window.location.hash = "#/quotes/new";
  });
  await expect(page.getByTestId("qb-compose-customer")).toHaveCount(0);
  await expect(page.getByTestId("qb-identity").locator("hos-customer-picker")).toBeVisible();
});

test("R11 P2-2 Change customer waits while the first create is out, so the pick is never lost", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST" });
  await pick(page);
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await create.asked;
  const change = page.getByTestId("qb-compose-change");
  await expect(change).toBeDisabled();
  await expect(page.getByTestId("qb-compose-making")).toContainText("being made for Maple Ridge Contracting");
  await create.release();
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-identity")).toContainText("Maple Ridge Contracting");
});

test("R11 P3 a first line added while another change is still saving says so instead of vanishing", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const made = creates(page);
  await pick(page);
  await page.locator("qd-quote-workarea").evaluate((el) => {
    (el as unknown as { busy: boolean }).busy = true;
  });
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-error")).toContainText("still being saved");
  await expect(page.getByTestId("qb-error")).not.toContainText("Pick a customer");
  expect(made).toEqual([]);
});

test("R13 a slow first create says Adding the product to the new quote, and Added only once the quote and the line both landed", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST" });
  await pick(page);
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await create.asked;
  const status = page.getByTestId("search-add-status");
  await expect(status).toContainText(/^Adding .+ to the new quote\.$/);
  await expect(page.getByText(/\bAdded\b/)).toHaveCount(0);
  await create.release();
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(status).toContainText(/^Added /);
});

test("R13 a refused first create ends the Adding word, it does not stay as if the line were going in", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST", refuse: { status: 422, error: "Not now." } });
  await pick(page);
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await create.asked;
  await create.release();
  await expect(page.getByTestId("qb-error")).toBeVisible();
  await expect(page.getByTestId("search-add-status")).not.toContainText("Adding");
});
