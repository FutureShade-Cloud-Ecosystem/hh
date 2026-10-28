// UX-106's audit shots: the Dealer Admin pricing and staff surfaces at a
// phone's width and at the desk's, light and charcoal. It runs only when
// DA_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-106-shots
//
// The names are the item's: <section>__<state>__<theme>-<width>.png. The
// charcoal half is the design system's brand (dark) theme, one press of the
// band's theme control away from the light default.
//
// Each shot waits for its own section's content, never for a control: a pill
// or a heading can land while the data is still loading, and the shot would
// prove nothing.
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");

const SETTLE = {
  staff: '[data-test="staff-count"], [data-test="staff-empty"]',
  margin: '[data-test="pr-margin-count"], [data-test="pr-margin-error"]',
  terms: '[data-test="pricing-terms-empty"]',
} as const;

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
];
const THEMES = ["light", "charcoal"] as const;

async function openDealerAdmin(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator(SETTLE.staff).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

// The section opens through the app's own list; on a phone the list waits
// folded behind the Settings control (UX-104), so the walk opens the control
// first.
async function openSection(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The pricing view a walk stands on: the strip's pill at the desk widths, the phone's select below them. */
async function openPricingView(page: Page, id: string, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab[data-tab="${id}"]`).click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function take(page: Page, section: string, state: string, theme: string, width: number): Promise<void> {
  await page.screenshot({
    path: join(dir, `${section}__${state}__${theme}-${width}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the pricing and staff surfaces at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }
      await take(page, "staff", "register", theme, size.width);
      await openSection(page, "pricing", SETTLE.margin);
      await take(page, "pricing", "margin-policy", theme, size.width);
      await openPricingView(page, "payment-terms", "Payment terms and deposits", SETTLE.terms);
      await take(page, "pricing", "payment-terms", theme, size.width);
    });
  }
}
