// The list page's title row and toolbar carry one control height: the New
// quote button in the title row, and the toolbar's search box, segmented
// control and Refresh button beside each other. The demo polish round 2
// found a control that shrank to its content while its neighbours stood at
// the control face height (then the form's customer search; the same
// discipline holds for the new page's controls).
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

test("list-toolbar-one-control-height", async ({ page }) => {
  await signIn(page);
  const toolbar = page.getByTestId("qd-list-toolbar");
  await expect(toolbar).toBeVisible();
  const tokenPx = await inputHeightToken(page);
  const controls: Array<[string, ReturnType<Page["locator"]>]> = [
    ["search input", toolbar.locator('input[data-control="text"][data-field="list-search"]')],
    ["segmented control", toolbar.locator('[data-control="segmented"][data-field="status-filter"]')],
    ["Refresh button", toolbar.locator('button.btn-secondary[data-action="refresh"]')],
    ["New quote button", page.locator('button.btn-primary[data-action="new-quote"]')],
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
