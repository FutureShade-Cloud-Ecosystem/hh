// Round 10 item 1 (rows 1034, 1092): Preview renders the customer page's own element in the desk from the
// customer-view read and never sends. A leg reads the status before and after, and the writes the page made.
import { expect, test } from "@playwright/test";
import { open, seed, settle, signIn, stubBrowserChannels, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("Preview shows the customer page in the desk and sends nothing", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const before = (await page.getByTestId("qb-status").innerText()).trim();
  const seen = writes(page);
  const reads: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "GET" && /\/customer-view$/.test(new URL(r.url()).pathname)) reads.push(r.url());
  });
  await page.getByTestId("qb-preview").click();
  const pane = page.getByTestId("qb-preview-pane");
  await expect(pane).toBeVisible();
  await expect(pane.locator("pd-quote-public").getByText("Preview: this is what your customer will see. Nothing has been sent.")).toHaveCount(1);
  // The customer page's own element, showing the quote it was handed.
  await expect(pane.locator("pd-quote-public")).toHaveCount(1);
  await expect(pane.locator("pd-quote-public").getByText("Q-1004").first()).toBeVisible();
  await expect(pane.locator("pd-quote-public").locator('[data-action="accept"]')).toHaveCount(0);
  await settle(page);
  expect(reads).toHaveLength(1);
  expect(seen.filter((w) => w.includes("/share-links") || w.includes("/sms-share"))).toEqual([]);
  expect(await page.evaluate(() => (window as unknown as { __opened: string[] }).__opened)).toEqual([]);
  expect((await page.getByTestId("qb-status").innerText()).trim()).toBe(before);
  // Close returns to the quote, still unsent.
  await pane.getByTestId("qb-preview-close").click();
  await expect(pane).toHaveCount(0);
  expect((await page.getByTestId("qb-status").innerText()).trim()).toBe(before);
});

test("Copy link and Send say on their buttons that they send", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-copy-link")).toContainText(/sends|marks.*sent/i);
  await expect(page.getByTestId("qb-send")).toContainText("SEND QUOTE");
  await expect(page.getByTestId("qb-preview")).not.toContainText(/sends|marks.*sent/i);
});

test("a preview closes when another quote opens", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await page.getByTestId("qb-preview").click();
  await expect(page.getByTestId("qb-preview-pane")).toBeVisible();
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-preview-pane")).toHaveCount(0);
});
