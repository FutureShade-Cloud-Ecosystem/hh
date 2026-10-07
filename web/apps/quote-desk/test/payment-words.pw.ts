// Round 11 item 4: the counter payment panel's words follow the customer's account, not one sentence for everyone.
import { expect, test } from "@playwright/test";
import { hold, settle, showList, signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function openQuote(page: import("@playwright/test").Page, no: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: no }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText(no);
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toBeVisible();
}

test("an account customer's accepted quote says to convert first, and offers no payment form", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003");
  const box = page.locator("qd-order-payment").getByTestId("op-box");
  await expect(box).toContainText("30 days");
  await expect(box).not.toContainText("no credit account");
  await expect(box).toContainText("Convert to Order");
  await expect(page.locator("qd-order-payment").getByTestId("op-tenders")).toHaveCount(0);
});

test("a Cash On Demand customer's accepted quote says so, and offers the one action", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-2102");
  const box = page.locator("qd-order-payment").getByTestId("op-box");
  await expect(box).toContainText("Cash On Demand");
  await expect(box).toContainText("no credit account");
  await expect(page.locator("qd-order-payment").getByTestId("op-tenders")).toBeVisible();
});

test("guard payment words: an account read that answers after another quote opened does not change its words", async ({ page }) => {
  await signIn(page);
  const slow = await hold(page, /\/api\/v1\/customers\/c0000000-0000-4000-9000-000000000001$/, { method: "GET" });
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
  await slow.asked;
  await openQuote(page, "Q-2102");
  const box = page.locator("qd-order-payment").getByTestId("op-box");
  await expect(box).toContainText("Cash On Demand");
  await slow.release();
  await settle(page);
  await expect(box).toContainText("Cash On Demand");
  await expect(page.locator("qd-order-payment").getByTestId("op-tenders")).toBeVisible();
});

test("the demo's Cash On Demand account (zero-day terms and a credit line) is told it is Cash On Demand and to convert first", async ({ page }) => {
  const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "ACCEPTED", customer_id: "c-cod-terms" } });
  const id = ((await seeded.json()) as { id: string }).id;
  await signIn(page);
  await page.evaluate((q) => {
    window.location.hash = `#/quotes/${q}`;
  }, id);
  const box = page.locator("qd-order-payment").getByTestId("op-box");
  await expect(box).toBeVisible();
  await expect(box).toContainText("Cash On Demand");
  await expect(box).not.toContainText("on account (0 days)");
  await expect(box).toContainText("Convert to Order");
  await expect(page.locator("qd-order-payment").getByTestId("op-tenders")).toHaveCount(0);
  // The guided surface says the same about the terms.
  await page.evaluate((q) => {
    window.location.hash = `#/quotes/${q}/convert`;
  }, id);
  await page.getByTestId("qc-next").click();
  await page.getByTestId("qc-next").click();
  await expect(page.getByTestId("qc-terms")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-terms")).not.toContainText("0 days");
});
