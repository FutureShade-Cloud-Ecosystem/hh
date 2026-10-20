// Round 10 item 13 (rows 1066 and 1092): focus and title edges. An unreadable quote and the convert route put
// focus on their own heading, not on the page body; the tab title does not keep the last quote's number after
// Sign out; and a read that failed for a reason that may pass says so and offers a retry, while a quote the server
// says is not there says it may not exist.
import { expect, test, type Page } from "@playwright/test";
import { openAccountMenu } from "./account-menu";
import { open, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

/** The heading text of the element that holds focus, piercing shadow roots; "BODY" when nothing does. */
async function focusedWords(page: Page): Promise<string> {
  return page.evaluate(() => {
    let el: Element | null = document.activeElement;
    while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement;
    return el === null || el === document.body ? "BODY" : (el.textContent ?? "").trim().slice(0, 60);
  });
}

test("an unreadable quote puts focus on its heading", async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => {
    window.location.hash = "#/quotes/no-such-quote";
  });
  await expect(page.getByTestId("qb-title")).toHaveText("This quote could not be opened");
  await expect.poll(() => focusedWords(page)).toContain("This quote could not be opened");
});

test("the convert route puts focus on its heading", async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => {
    window.location.hash = "#/quotes/q-1003/convert";
  });
  await expect(page.getByTestId("qc-title")).toBeVisible();
  await expect.poll(() => focusedWords(page)).toContain("Convert");
});

test("after Sign out the tab title does not keep the last quote's number", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await expect.poll(() => page.title()).toBe("Gable Landscape Supply · Q-1001");
  await openAccountMenu(page);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  expect(await page.title()).toBe("Gable Landscape Supply · Quote Desk");
});

test("a read that failed in passing offers a retry, and a quote that is not there says it may not exist", async ({ page }) => {
  await signIn(page);
  let failures = 1;
  await page.route(/\/api\/v1\/quotes\/q-1001$/, (route) => {
    if (route.request().method() === "GET" && failures > 0) {
      failures -= 1;
      return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "quote: the store is busy" }) });
    }
    return route.continue();
  });
  await page.evaluate(() => {
    window.location.hash = "#/quotes/q-1001";
  });
  await expect(page.getByTestId("qb-unreadable")).toContainText("could not be read right now");
  await expect(page.getByTestId("qb-unreadable")).not.toContainText("may not exist");
  await page.getByTestId("qb-retry-read").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await page.evaluate(() => {
    window.location.hash = "#/quotes/no-such-quote";
  });
  await expect(page.getByTestId("qb-unreadable")).toContainText("may not exist");
  await expect(page.getByTestId("qb-retry-read")).toHaveCount(0);
});
