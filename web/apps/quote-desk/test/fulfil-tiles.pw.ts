// The fulfilment tiles' box in the summary panel: the Pickup and Delivery
// tiles must sit inside the panel's visible area at every count of the
// quote's items, on both picks, with the tile's label visible. The demo
// polish round found them clipped under the pinned totals foot in the sent
// shot set (sales-05-sent): the panel's scroll zone loses the tiles' box
// behind the pinned foot, and the panel's own overflow hides it.
//
// The legs run at zero items (floor zero): the quote is open with no lines,
// where the tiles must still show their labels rather than vanish. Each leg
// also proves the state stays true with a line on the quote, sent, the state
// the shot set shipped.
import { expect, test, type Page } from "@playwright/test";

test.use({ viewport: { width: 1440, height: 900 } });

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

/** Open a quote with no lines: the compose picker's pick IS the create. */
async function openQuoteWithNoItems(page: Page): Promise<void> {
  await signIn(page);
  const pickerInput = page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…");
  await pickerInput.fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-fulfil-pickup")).toBeVisible();
}

/**
 * The tile's whole box must sit above the totals card (nothing of it hides
 * behind or below the card), fully inside the summary panel's visible box,
 * and the label's own box must sit inside the tile with the tile's padding
 * (nothing of the word is cut).
 */
async function expectTileUnclipped(page: Page, testId: string, label: string): Promise<void> {
  const tile = page.getByTestId(testId);
  await expect(tile).toBeVisible();
  const totalsTop = await page.getByTestId("qb-totals").evaluate((el) => el.getBoundingClientRect().top);
  const tileBox = await tile.boundingBox();
  expect(tileBox).toBeTruthy();
  expect(
    tileBox!.y + tileBox!.height,
    "the tile's whole box sits above the totals card: no part hides behind or below it",
  ).toBeLessThanOrEqual(totalsTop + 0.5);
  const panelBox = await page.locator("qd-quote-workarea .summary").evaluate((el) => {
    const r = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    return { bottom: r.bottom, paddingBottom: parseFloat(style.paddingBottom) || 0 };
  });
  expect(
    tileBox!.y + tileBox!.height,
    "the tile's box ends inside the summary panel's visible area",
  ).toBeLessThanOrEqual(panelBox.bottom - panelBox.paddingBottom + 0.5);
  const labelBox = await tile.evaluate((el) => {
    const range = document.createRange();
    range.selectNodeContents(el);
    const r = range.getBoundingClientRect();
    const t = el.getBoundingClientRect();
    return { top: r.top, bottom: r.bottom, tilePaddingBottom: t.bottom - r.bottom };
  });
  expect(labelBox.tilePaddingBottom, "the label sits inside the tile with the tile's own padding").toBeGreaterThanOrEqual(4);
  expect(labelBox.bottom, `the ${label} label's box ends inside the tile's box`).toBeLessThanOrEqual(tileBox!.y + tileBox!.height + 0.5);
}

test("floor-zero-no-clipped-pickup", async ({ page }) => {
  await openQuoteWithNoItems(page);
  await expectTileUnclipped(page, "qb-fulfil-pickup", "Pickup");
  // The state stays true under the count the demo shipped: one line, sent.
  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await page.getByTestId("qb-send").click();
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
  await expectTileUnclipped(page, "qb-fulfil-pickup", "Pickup");
});

test("floor-zero-no-clipped-delivery", async ({ page }) => {
  await openQuoteWithNoItems(page);
  await page.getByTestId("qb-fulfil-delivery").click();
  await expect(page.getByTestId("qb-fulfil-delivery")).toHaveAttribute("aria-pressed", "true");
  await expectTileUnclipped(page, "qb-fulfil-delivery", "Delivery");
  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await page.getByTestId("qb-send").click();
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
  await expectTileUnclipped(page, "qb-fulfil-delivery", "Delivery");
});
