// Round 10 item 12 (row 1066): a floor refusal that lands after the person switched quote, started a new quote or
// walked to another screen is never silent. It is said on the list ("a change to Q was refused") and again on the
// quote itself when the person comes back.
import { expect, test } from "@playwright/test";
import { hold, open, seed, settle, showList, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function discountHeld(page: import("@playwright/test").Page) {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1001");
  const held = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/1$/, { method: "PATCH" });
  const disc = page.getByTestId("qb-line").first().locator('[data-field="disc"]');
  await disc.fill("30");
  await disc.blur();
  await held.asked;
  return held;
}

async function saidOnListAndOnReturn(page: import("@playwright/test").Page): Promise<void> {
  await expect(page.getByTestId("qb-error")).toHaveText("");
  await showList(page);
  await expect(page.getByTestId("qd-list-notice")).toContainText("Q-1001");
  await expect(page.getByTestId("qd-list-notice")).toContainText("floor");
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByTestId("qb-error")).toContainText("floor");
}

test("a floor refusal that lands after the person moved to another quote is said on the list and on return", async ({ page }) => {
  const held = await discountHeld(page);
  await open(page, "Q-1004");
  await held.release();
  await settle(page);
  await saidOnListAndOnReturn(page);
});

test("a floor refusal that lands after the person moved to a new quote is said on the list and on return", async ({ page }) => {
  const held = await discountHeld(page);
  await page.getByRole("button", { name: "New Quote" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  await held.release();
  await settle(page);
  await saidOnListAndOnReturn(page);
});

test("a floor refusal that lands while the convert screen is open is said on the list", async ({ page }) => {
  const held = await discountHeld(page);
  await page.evaluate(() => {
    window.location.hash = "#/quotes/q-1003/convert";
  });
  await expect(page.getByTestId("qc-title")).toBeVisible();
  await held.release();
  await settle(page);
  await page.evaluate(() => {
    window.location.hash = "#/quotes";
  });
  await expect(page.getByTestId("qd-list-notice")).toContainText("Q-1001");
});
