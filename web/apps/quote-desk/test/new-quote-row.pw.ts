// The New quote row carries one control height: the customer search input
// rides beside the branch select, the expiry date input and the Create
// button, under the filter row's status select and Refresh button, and the
// design system's control face is one height for a form row. The demo
// polish round 2 shot set found the search input shorter than the controls
// beside it: the picker's wiring let its input shrink to its content while
// the row's design system controls stood at the control face height.
//
// Every control's box height is pinned inside a tolerance of the design
// system's input height token, read live off the document root (no
// arbitrary figure), and the row reads as one height: the control heights
// agree with each other, so a shorter wiring cannot hide behind the token.
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

/**
 * The design system's input height token, resolved live off the root. The
 * caller runs the control bounds first and asserts the token's presence
 * last, so a tree without the token still reports the row's own heights.
 */
async function inputHeightToken(page: Page): Promise<number> {
  return page.evaluate(() => {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--hh-h-input").trim();
    return parseFloat(raw) || 0;
  });
}

/** The token's presence leg: the bounds mean nothing without it. */
async function expectTokenResolves(page: Page, tokenPx: number): Promise<void> {
  expect(tokenPx, "the design system's input height token --hh-h-input resolves live on the document root").toBeGreaterThan(0);
}

test("new-quote-row-one-control-height", async ({ page }) => {
  await signIn(page);
  const card = page.locator("qd-quote-builder .box").filter({ has: page.getByTestId("qb-list-filter") });
  await expect(card).toBeVisible();
  const tokenPx = await inputHeightToken(page);
  const controls: Array<[string, ReturnType<Page["locator"]>]> = [
    ["customer search input", card.locator('fieldset[data-control="field-group"] hos-customer-picker input')],
    ["status select", card.locator('select[data-control="select"][data-field="status-filter"]')],
    ["branch select", card.locator('select[data-control="select"][data-field="new-branch"]')],
    ["expiry date input", card.locator('input[type="date"][data-control="date"][data-field="new-expiry"]')],
    ["Refresh button", card.locator('button.btn-secondary[data-action="refresh"]')],
    ["Create button", card.locator('button.btn-primary[data-action="create"]')],
  ];
  const heights: number[] = [];
  for (const [label, control] of controls) {
    await expect(control).toBeVisible();
    const box = await control.boundingBox();
    expect(box).toBeTruthy();
    heights.push(box!.height);
    expect(
      box!.height,
      `the ${label}'s box height (${box!.height.toFixed(2)}px) sits inside the tolerance of the design system's input height token (${tokenPx}px, live)`,
    ).toBeGreaterThanOrEqual(tokenPx - 1);
    expect(
      box!.height,
      `the ${label}'s box height (${box!.height.toFixed(2)}px) sits inside the tolerance of the design system's input height token (${tokenPx}px, live)`,
    ).toBeLessThanOrEqual(tokenPx + 1);
  }
  const spread = Math.max(...heights) - Math.min(...heights);
  expect(
    spread,
    `one height across the row: the control heights are ${heights.map((h) => h.toFixed(2)).join(", ")}`,
  ).toBeLessThanOrEqual(1);
  await expectTokenResolves(page, tokenPx);
});
