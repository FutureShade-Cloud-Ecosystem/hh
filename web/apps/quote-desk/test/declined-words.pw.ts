// Round 11 item 5: a declined quote shows why and how it was declined, and the change history speaks in words:
// "Status: Draft to Sent", never the codes, and never a row for the base quantity the person did not change.
import { expect, test } from "@playwright/test";
import { signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function sentQuote(page: import("@playwright/test").Page): Promise<string> {
  const response = await page.request.post("/_stub/seed-quote", { data: { status: "SENT", customer_id: "c-fenwick" } });
  const id = ((await response.json()) as { id: string }).id;
  await signIn(page);
  await page.evaluate((q) => {
    window.location.hash = `#/quotes/${q}`;
  }, id);
  await expect(page.getByTestId("qb-context")).toBeVisible();
  return id;
}

test("a declined quote says why, how and what was written", async ({ page }) => {
  const id = await sentQuote(page);
  await page.getByTestId("qb-decision-reject-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("text");
  await page.getByTestId("qb-decision-reason").selectOption("competitor");
  await page.getByTestId("qb-decision-note").fill("Bought from the other yard");
  await page.getByTestId("qb-decision-reject").click();
  await expect(page.getByTestId("qb-status")).toContainText(/lost/i);
  const declined = page.getByTestId("qb-declined");
  await expect(declined).toBeVisible();
  await expect(declined).toContainText("Went with a competitor");
  await expect(declined).toContainText("by text");
  await expect(declined).toContainText("Bought from the other yard");
  // It is the server's record: it is there again when the quote is opened afresh.
  await page.evaluate(() => {
    window.location.hash = "#/quotes";
  });
  await page.evaluate((q) => {
    window.location.hash = `#/quotes/${q}`;
  }, id);
  await expect(page.getByTestId("qb-declined")).toContainText("Went with a competitor");
});

test("the change history says Sent and Accepted in words, and shows no base quantity row", async ({ page }) => {
  await sentQuote(page);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await page.getByTestId("qb-decision-statement").check();
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-status")).toContainText(/accepted/i);
  await page.getByText("Quote detail & admin").click();
  const history = page.locator("qd-quote-history");
  await expect(history.getByTestId("qh-group").first()).toBeVisible();
  const text = (await history.evaluate((el) => el.shadowRoot?.textContent ?? "")).replace(/\s+/g, " ");
  expect(text).toContain("Status: Sent → Accepted");
  expect(text).not.toMatch(/\b(DRAFT|SENT|ACCEPTED|LOST|CONVERTED|EXPIRED)\b/);
  expect(text).not.toContain("Quantity (base)");
});
