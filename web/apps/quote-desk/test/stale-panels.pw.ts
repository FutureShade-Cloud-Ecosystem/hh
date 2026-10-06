// The panels under the detail disclosure forget the last quote when another
// opens: a refusal, a notice, a lapse read, availability lines and pasted text
// belong to the quote they were made for. A read that fails is said, never
// shown as an empty answer.
import { expect, test, type Page } from "@playwright/test";
import { openTab } from "./surface-tabs";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string, tab: "activity" | "stock"): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  await openTab(page, tab);
  await expect(page.getByTestId("qb-context")).toContainText(text);
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the follow-ups refusal of one quote is gone when another opens", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002", "activity");
  const panel = page.locator("qd-quote-followups");
  await panel.getByRole("button", { name: "Text the quote link" }).click();
  await expect(panel.getByTestId("fu-error")).toBeVisible();
  await openQuote(page, "Q-1001", "activity");
  await expect(panel.getByTestId("fu-error")).toHaveCount(0);
});

test("the re-hold notice of one quote does not persist onto the next", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1001", "stock");
  const panel = page.locator("qd-quote-hold-expiry");
  await panel.getByRole("button", { name: "Re-hold these lines" }).click();
  await expect(panel.getByTestId("he-notice")).toHaveText("Re-held 1 line.");
  await openQuote(page, "Q-1003", "stock");
  await expect(panel.getByTestId("he-notice")).toHaveCount(0);
  await expect(panel.getByTestId("he-box")).toHaveCount(0);
});

test("a hold expiry read that fails says so, and is not shown as no lapse", async ({ page }) => {
  await page.route("**/api/v1/quotes/q-1001/hold-expiry", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the hold store is down" }) }),
  );
  await signIn(page);
  await openQuote(page, "Q-1001", "stock");
  await expect(page.locator("qd-quote-hold-expiry").getByTestId("he-unread")).toContainText("could not be read");
});

test("availability keeps nothing of the previous quote when its read fails", async ({ page }) => {
  await page.route("**/api/v1/quotes/q-1001/availability", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the stock read is down" }) }),
  );
  await signIn(page);
  await openQuote(page, "Q-1002", "stock");
  const panel = page.locator("qd-quote-availability");
  await expect(panel.getByTestId("av-chip").first()).toHaveText("400 available");
  await openQuote(page, "Q-1001", "stock");
  await expect(panel.getByTestId("av-error")).toBeVisible();
  await expect(panel.getByTestId("av-chip")).toHaveCount(0);
});

test("the pasted job text is cleared when another quote opens", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001", "activity");
  const box = page.locator("qd-quote-assist").locator("#jobtext");
  await box.fill("200 sq ft of pavers for a patio");
  await expect(box).toHaveValue("200 sq ft of pavers for a patio");
  await openQuote(page, "Q-1003", "activity");
  await expect(box).toHaveValue("");
});
