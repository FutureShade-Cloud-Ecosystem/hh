// UX-121 round 2: the register's chip row, the pills that count the loaded rows.
//
// At 1440 the chip row is ONE line: the row's own height is one chip's height. The chips are a
// control row, and a control row that wraps is a row a person has to hunt through.
//
// At 390 the chip row takes no more rows than the chip row on main took: the register on a phone
// is no taller to read than it was before this item.
//
// The legs measure the boxes the browser lays out, the way a person reads them: one row is one
// line of chip tops, and the row's height is the height of the chips standing on it.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, controllerHandoff, resetStub, openTab } from "./gl-helpers.js";

const THEMES = ["light", "charcoal"] as const;

/**
 * Main's chip row at 390, measured on main's own view (origin/main, where the chips carry the
 * bare counts) with this same register: the chips took this many rows there, and this item must
 * not make the register taller to read on a phone.
 */
const MAIN_ROWS_AT_390 = 2;

/**
 * Signs in as the Financial Controller and stands on the register, with a register full enough to
 * count: the older journals are planted, so the chips carry the counts a real register shows
 * ("Posted 100"), the counts that are long enough to wrap a chip row.
 */
async function openRegister(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  const planted = await request.post("/api/v1/gl/journals/_stub/planter", {
    data: { count: 130 },
    headers: { Authorization: "Bearer the-planter-is-a-stub-control" },
  });
  if (!planted.ok()) throw new Error("the stub refused the older journals");
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-approval-id]")).toHaveCount(5);
  await openTab(page, "journals");
  await expect(page.getByRole("table", { name: "Journal register" })).toBeVisible();
}

/** The charcoal half: one tap on the header's account menu, then the theme. */
async function charcoalOn(page: Page): Promise<void> {
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
}

/** The rows the chips stand on: one row is one line of chip tops. */
async function chipRows(page: Page): Promise<number> {
  return page.locator(".gl-filters .gl-filter").evaluateAll((nodes) => {
    const tops = new Set(nodes.map((node) => Math.round(node.getBoundingClientRect().top)));
    return tops.size;
  });
}

for (const theme of THEMES) {
  test(`at 1440 the chip row is one line, ${theme}`, async ({ page, request }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1440, height: 900 });
    await openRegister(page, request);
    if (theme === "charcoal") await charcoalOn(page);

    const row = page.locator(".gl-filters");
    const chip = page.locator(".gl-filters .gl-filter").first();
    const rowHeight = Math.round((await row.boundingBox())?.height ?? 0);
    const chipHeight = Math.round((await chip.boundingBox())?.height ?? 0);
    expect(rowHeight, `the chip row is ${rowHeight} px tall and a chip is ${chipHeight} px tall`).toBe(chipHeight);
    expect(await chipRows(page), "the chips stand on more than one line").toBe(1);
  });

  test(`at 390 the chip row takes no more rows than main's did, ${theme}`, async ({ page, request }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 390, height: 844 });
    await openRegister(page, request);
    if (theme === "charcoal") await charcoalOn(page);

    const rows = await chipRows(page);
    expect(rows, `the chip row takes ${rows} rows, and main's took ${MAIN_ROWS_AT_390}`).toBeLessThanOrEqual(MAIN_ROWS_AT_390);
  });
}
