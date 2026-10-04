// A re-read of the quote never wipes what a person is typing. The work area
// re-reads the quote on every fan-out; a late re-read used to reset the
// discount and quantity drafts and, with them, the box under the person's
// hands, so the blur that should have saved the figure saw an empty box and
// nothing was written. The suite's discount test lost to that race about as
// often as it won; this test makes the race deterministic.
import { expect, test } from "@playwright/test";

// Both tests read the second line of the seeded draft: a quote another test left behind, or a layout row that resumes
// another quote, would put a different quote under the click, so each starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a late re-read leaves the box being typed in alone, and the blur still saves", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  const disc = page.getByTestId("qb-line-disc").nth(1);
  await expect(disc).toBeVisible();
  await disc.fill("5");
  // The fan-out the builder runs after any edit elsewhere on the page, and its read.
  const read = page.waitForResponse((r) => /\/api\/v1\/quotes\/q-\d+$/.test(r.url()) && r.request().method() === "GET");
  await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    root?.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-1001" } }));
  });
  await read;
  await page.waitForTimeout(300);
  await expect(disc).toHaveValue("5");
  await disc.blur();
  await expect(page.getByTestId("qb-totals")).toContainText("Discount");
});

test("the edit panel keeps the figure being typed through a re-read too", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByText("Quote detail & admin").click();
  const qty = page.locator("qd-quote-edit").locator('[data-field="qty"]').nth(1);
  await expect(qty).toBeVisible();
  await qty.fill("7");
  const read = page.waitForResponse((r) => /\/api\/v1\/quotes\/q-\d+$/.test(r.url()) && r.request().method() === "GET");
  await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    root?.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-1001" } }));
  });
  await read;
  await page.waitForTimeout(300);
  await expect(qty).toHaveValue("7");
  // A box nobody is typing in returns to the server's figure after the same re-read.
  await page.locator("qd-quote-edit").locator('[data-field="qty"]').first().focus();
  await qty.fill("9");
  await page.locator("qd-quote-edit").locator('[data-field="qty"]').first().focus();
  await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    root?.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-1001" } }));
  });
  await expect(qty).toHaveValue("2");
});
