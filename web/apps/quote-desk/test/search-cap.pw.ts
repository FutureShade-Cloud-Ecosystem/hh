// Round 10 item 15 (row 1066): a search reads up to 40 pages of the list a keystroke; at that cap the list says
// so, in words, instead of ending silently as if everything had been read.
import { expect, test } from "@playwright/test";
import { signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.put("/_stub/quote-page-size", { data: { size: 0 } });
  await request.post("/_stub/reset");
});

test("a search that reaches the page cap says it stopped, and a search that ends does not", async ({ page, request }) => {
  await request.put("/_stub/quote-page-size", { data: { size: 1 } });
  await signIn(page);
  const box = page.locator('[data-field="list-search"]');
  // The seeded list is short: the search reads to its end and says nothing of a cap.
  await box.fill("Q-1001");
  await expect(page.getByTestId("qb-list-row").first()).toBeVisible();
  await page.waitForTimeout(600);
  await expect(page.getByTestId("qd-search-capped")).toHaveCount(0);
  // More quotes than the cap reads: the search stops at the cap and says so.
  for (let n = 0; n < 45; n += 1) await request.post("/_stub/seed-quote", { data: { customer_id: "c-fenwick" } });
  await box.fill("");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.waitForTimeout(400);
  await box.fill("no-such-customer-anywhere");
  await expect(page.getByTestId("qd-search-capped")).toContainText("40 pages");
  await expect(page.getByTestId("qd-search-capped")).toContainText("more quotes");
});
