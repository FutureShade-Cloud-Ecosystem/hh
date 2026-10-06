// New Quote's empty state: the compose card is the screen (one card, one
// question), and the space between it and the quotes list under the desk is
// bounded by a spacing token, not a wrapper's reserved height. The demo
// round found a large void there (defect 3): the compose wrapper reserved a
// fixed minimum taller than its card, and the void read as a broken layout.
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

/** The visible gap between the compose card's bottom and the quotes box's top. */
async function composeGap(page: Page): Promise<number> {
  const cardBox = await page.getByTestId("qb-identity").boundingBox();
  expect(cardBox).toBeTruthy();
  const quotesBox = page.locator("qd-quote-builder .box").filter({ has: page.getByTestId("qb-list-filter") });
  const boxTop = await quotesBox.evaluate((el) => el.getBoundingClientRect().top);
  return boxTop - (cardBox!.y + cardBox!.height);
}

async function spacingToken(page: Page): Promise<number> {
  const tokenPx = await page.evaluate(() => {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--hh-s-64").trim();
    return parseFloat(raw) || 0;
  });
  expect(tokenPx, "the desk's largest spacing token resolves").toBeGreaterThan(0);
  return tokenPx;
}

test("the compose card's gap to the quotes list is bounded by the spacing token", async ({ page }) => {
  await signIn(page);
  // The single empty-state card is the screen.
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const gap = await composeGap(page);
  const tokenPx = await spacingToken(page);
  expect(gap, "the visible gap between the compose card's bottom and the quotes list's top").toBeLessThanOrEqual(tokenPx);
});

test("an empty list still reads its quiet message inside the bounded layout", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-list-filter").selectOption("LOST");
  await expect(page.getByTestId("qb-list")).toHaveText("No quotes.");
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const gap = await composeGap(page);
  const tokenPx = await spacingToken(page);
  expect(gap).toBeLessThanOrEqual(tokenPx);
});
