// A slow read of the quote that was open before never lands over the quote
// open now, and a save never goes to the wrong quote: the edit panel and the
// change history each answer only for the quote that asked.
import { expect, test, type Page } from "@playwright/test";
import { showList } from "./switch-helpers.js";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

const edit = (page: Page) => page.locator("qd-quote-edit");
const history = (page: Page) => page.locator("qd-quote-history");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the edit panel shows the quote open now when the previous quote's slow read lands late, and a save goes to it", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003");
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  const writes: string[] = [];
  page.on("request", (req) => {
    if (req.method() !== "GET" && req.url().includes("/api/v1/quotes/")) writes.push(`${req.method()} ${new URL(req.url()).pathname}`);
  });
  // The read of Q-1001 is slow.
  await page.route("**/api/v1/quotes/q-1001", async (route) => {
    if (route.request().method() === "GET") await new Promise((resolve) => setTimeout(resolve, 900));
    await route.continue();
  });
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  await page.waitForTimeout(1300);
  // The late answer for Q-1001 changed nothing: still Q-1003, no editable line of Q-1001 on screen.
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  await expect(edit(page).locator('[data-field="qty"]')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test("the history shows no change of the previous quote under the quote open now", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(history(page).getByTestId("qh-group")).toHaveCount(1);
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
  await expect(history(page)).toContainText("No changes yet.");
  await page.route("**/api/v1/quotes/q-1001/edits", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 900));
    await route.continue();
  });
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
  await page.waitForTimeout(1300);
  await expect(history(page)).toContainText("No changes yet.");
  await expect(history(page).getByTestId("qh-group")).toHaveCount(0);
});
