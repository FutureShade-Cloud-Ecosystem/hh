// New Quote's empty state under the split-list navigation: the compose page
// is the work area's own screen (one card, one question), and the quotes
// list is NOT on it any more (the CP1 feedback: the list and the quote flow
// on separate screens). The old void defect (a wrapper reserving a fixed
// minimum taller than its card) is pinned the same way it was: the compose
// wrapper's height is bounded by a spacing token, not a reserved box.
import { expect, test, type Page } from "@playwright/test";

const SALES = { name: "Sales", exact: true };

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", SALES).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The compose card's own height, the wrapper's height, and the page's own height. */
async function heights(page: Page): Promise<{ card: number; wrap: number; page: number }> {
  return page.evaluate(() => {
    const area = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot?.querySelector("qd-quote-workarea");
    const card = area?.shadowRoot?.querySelector('[data-testid="qb-identity"]');
    const wrap = area?.shadowRoot?.querySelector(".composewrap");
    return {
      card: card instanceof HTMLElement ? card.getBoundingClientRect().height : -1,
      wrap: wrap instanceof HTMLElement ? wrap.getBoundingClientRect().height : -1,
      page: document.documentElement.scrollHeight,
    };
  });
}

async function spacingToken(page: Page): Promise<number> {
  const tokenPx = await page.evaluate(() => {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--hh-s-64").trim();
    return parseFloat(raw) || 0;
  });
  expect(tokenPx, "the desk's largest spacing token resolves").toBeGreaterThan(0);
  return tokenPx;
}

test("the compose page is the card alone, with no list under it", async ({ page }) => {
  await signIn(page);
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  await expect(page).toHaveURL(/#\/quotes\/new$/);
  // The quotes list is not on the compose page: its markup shows nothing.
  await expect(page.getByTestId("qb-list")).toBeHidden();
  await expect(page.getByTestId("qd-list-toolbar")).toBeHidden();
  // The wrapper reserves no void taller than the card by more than a
  // spacing token (the old defect's own bound).
  const h = await heights(page);
  expect(h.card).toBeGreaterThan(0);
  const tokenPx = await spacingToken(page);
  expect(h.wrap - h.card, "the wrapper's reserved space over the card").toBeLessThanOrEqual(tokenPx);
});

test("an empty list still reads its quiet message on the list page", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Sent", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toHaveText("No quotes.");
  await expect(page.getByTestId("qb-identity")).toBeHidden();
  // The Lost tab too: LOST is a real status, and an empty answer reads the same.
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Lost", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toHaveText("No quotes.");
  await expect(page.getByTestId("qb-identity")).toBeHidden();
});
