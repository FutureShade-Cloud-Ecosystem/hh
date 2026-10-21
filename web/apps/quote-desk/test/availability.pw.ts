// The availability chips in a real browser, against the stubbed harness: a
// chip per line graded by the server and worded by the app (each grade with
// its own word, never colour alone), the cross branch peek behind a short
// line showing every branch row the server returns, the average cost only
// when the server sends one (never a zero), the unwired seam saying "not
// reported", and the panel re-reading when a line changes elsewhere.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { showList } from "./switch-helpers.js";

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.locator("qd-quote-availability").getByTestId("av-box")).toBeVisible();
}

const panel = (page: Page) => page.locator("qd-quote-availability");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a covered line is a green chip with its word and no peek", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  const chip = panel(page).getByTestId("av-chip").first();
  await expect(chip).toHaveAttribute("data-avail-grade", "GREEN");
  await expect(chip).toHaveText("400 available");
  await expect(panel(page).getByRole("button", { name: "Other branches" })).toHaveCount(0);
});

test("a thin line says so in words and its peek shows every branch the server returns", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Accepted");
  const thin = panel(page).getByTestId("av-line").filter({ has: page.locator('[data-avail-grade="THIN"]') });
  await expect(thin.getByTestId("av-chip")).toHaveText("only 2 of 3");
  await thin.getByRole("button", { name: "Other branches" }).click();
  const rows = panel(page).getByTestId("av-branch");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("PLM (this branch): 2 available");
  await expect(rows.nth(1)).toContainText("JUL: 60 available");
  // The peek serves quantities only: no cost, whoever the caller is.
  await expect(rows.nth(1)).not.toContainText("cost");
  await shot(panel(page), "availability-peek");
  // The peek closes again.
  await thin.getByRole("button", { name: "Other branches" }).click();
  await expect(panel(page).getByTestId("av-peek")).toHaveCount(0);
});

test("a branch with nothing is still a row, said as none available", async ({ page, request }) => {
  await request.put("/_stub/stock", { data: { product_id: "p-clear-stone", pointLoma: 2, julian: 0 } });
  await signIn(page);
  await openQuote(page, "Accepted");
  await panel(page).getByRole("button", { name: "Other branches" }).click();
  await expect(panel(page).getByTestId("av-branch")).toHaveCount(2);
  await expect(panel(page).getByTestId("av-branch").nth(1)).toContainText("JUL: none available");
});

test("the average cost is absent for an off list caller, and never a zero", async ({ page, request }) => {
  await request.put("/_stub/cost-list", { data: { member: false } });
  await signIn(page);
  await openQuote(page, "Accepted");
  await panel(page).getByRole("button", { name: "Other branches" }).click();
  await expect(panel(page).getByTestId("av-branch")).toHaveCount(2);
  await expect(panel(page).getByTestId("av-peek")).not.toContainText(/cost/i);
  await expect(panel(page).getByTestId("av-peek")).not.toContainText("$");
});

test("each grade carries its own word and look: none and thin and unreported are different answers", async ({ page, request }) => {
  await request.put("/_stub/stock", { data: { product_id: "p-clear-stone", pointLoma: 0 } });
  await signIn(page);
  await openQuote(page, "Accepted");
  const none = panel(page).getByTestId("av-chip").filter({ hasText: "none available" });
  await expect(none).toHaveAttribute("data-avail-grade", "NONE");
  await expect(none).toHaveClass(/red/);
  await request.put("/_stub/stock", { data: { product_id: "p-clear-stone", pointLoma: 2 } });
  await page.reload();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await openQuote(page, "Accepted");
  await expect(panel(page).getByTestId("av-chip").filter({ hasText: "only 2 of 3" })).toHaveClass(/amber/);
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await expect(panel(page).getByTestId("av-chip").first()).toHaveClass(/green/);
});

test("an unwired seam reads not reported on every line, with the warning, never none", async ({ page, request }) => {
  await request.put("/_stub/seam-wired", { data: { wired: false } });
  await signIn(page);
  await openQuote(page, "Accepted");
  await expect(panel(page).getByTestId("av-unwired")).toContainText("not the same as");
  for (const chip of await panel(page).getByTestId("av-chip").all()) {
    await expect(chip).toHaveText("not reported");
    await expect(chip).toHaveAttribute("data-avail-grade", "UNKNOWN");
  }
  await expect(panel(page).getByRole("button", { name: "Other branches" })).toHaveCount(0);
});

test("a stock change shows when quote-changed reaches the panel", async ({ page, request }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(panel(page).getByTestId("av-chip").first()).toHaveText("400 available");
  await request.put("/_stub/stock", { data: { product_id: "p-charcoal-60", pointLoma: 5 } });
  await panel(page).evaluate((el) => el.dispatchEvent(new CustomEvent("quote-changed", { bubbles: true, composed: true, detail: { quoteId: "q-1001" } })));
  await expect(panel(page).getByTestId("av-chip").first()).toHaveText("only 5 of 120");
});

// Visual review round 1, P3: a row said only its line number. It now names the
// product, read from the quote's own lines, beside the number.
test("each availability row names its product beside the line number", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  const firstName = (await page.locator("qd-quote-edit").getByTestId("qe-line").first().innerText()).trim();
  const row = panel(page).getByTestId("av-line").first();
  const label = (await row.locator("td").first().innerText()).trim();
  expect(label).toMatch(/^1\s*·\s*\S+/);
  const named = label.replace(/^1\s*·\s*/, "");
  expect(named.length).toBeGreaterThan(3);
  expect(firstName).toContain(named);
});
