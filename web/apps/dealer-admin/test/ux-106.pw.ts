// UX-106: the Dealer Admin pricing and staff surfaces at a phone's width.
//
//   1. Two rows of navigation: one axis, one control, and the content on the
//      first screen at 390 x 844.
//   2. A false selection: only the open view reads as selected, in both
//      themes, even with the pointer resting on another pill.
//   3. A raw id in a dealer screen, and a table cut off at the right edge.
//   4. Staff at 390: the register's role selects fit their card.
//
// One journey opens the app the way the audit's own shots did, so the shots
// and these proofs see the same screen.
import { expect, test, type Page } from "@playwright/test";

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
] as const;

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
}

/** The phone folds the surface list behind the Settings control (UX-104). */
async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The charcoal half is the design system's brand theme, one press away. */
async function goCharcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header .account").click();
  await page.locator('[data-testid="theme-control"]').click();
  await page.locator("hh-shell-header .account").click();
}

// -- 2: a false selection -----------------------------------------------------------------

for (const theme of ["light", "charcoal"] as const) {
  test(`only the open pricing view reads as selected, in ${theme}`, async ({ page }) => {
    await signIn(page);
    await openSurface(page, "pricing", "[data-test=pr-margin-count]");
    if (theme === "charcoal") await goCharcoal(page);

    // One navigation, one open view.
    const open = page.locator('.pr-tab[aria-current="true"]');
    await expect(open).toHaveCount(1);
    await expect(open).toHaveText("Margin policy");

    // The open pill carries the accent boundary. A pill the pointer rests on
    // must not wear it: the hover painted the same accent, so a hovered pill
    // read as a second selection.
    const accent = await open.evaluate((el) => getComputedStyle(el).borderTopColor);
    const other = page.locator('.pr-tab[aria-current="false"]').nth(3);
    await other.hover();
    await expect(other).toBeVisible();
    const wearing = await page.locator(".pr-tab").evaluateAll(
      (els, mark) => els.filter((el) => getComputedStyle(el).borderTopColor === mark).length,
      accent,
    );
    expect(wearing).toBe(1);
  });
}
