// Dealer Admin's audit screenshots (UX-104): the Staff, Pricing and
// Printing sections at 390 and 1440 px, light and charcoal, full page with
// the animations off and the page settled. It runs only when DA_SHOT_DIR
// names a folder (so the suite's own run skips it), and writes there and
// nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test shots
//
// The charcoal half is the design system's brand (dark) theme, one press of
// the band's theme control away from the light default.
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");

// Each section's settle marker is the section's own content, or its own
// explicit error, never a control: a pill or a heading can land while the
// data is still loading, and the shot would prove nothing (the round 1
// charcoal Pricing shot showed only the heading and the pills).
const SETTLE = {
  staff: '[data-test="staff-count"], [data-test="staff-empty"]',
  pricing: '[data-test="pr-margin-count"], [data-test="pr-margin-error"]',
  printing: '[data-test="print-idle"], [data-test="print-error"]',
} as const;

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function openDealerAdmin(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator(SETTLE.staff).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

// The section opens through the app's own list; on a phone the list waits
// folded behind the Settings control (UX-104), so the walk opens the
// control first. The settle marker is the section's own content, or its
// own explicit error (see SETTLE).
async function openSection(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function take(page: Page, section: string, theme: string, width: number): Promise<void> {
  await page.screenshot({
    path: join(dir, `dealer-admin__${section}__${theme}-${width}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the Staff, Pricing and Printing sections at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }
      await take(page, "staff", theme, size.width);
      await openSection(page, "pricing", SETTLE.pricing);
      await take(page, "pricing", theme, size.width);
      await openSection(page, "printing", SETTLE.printing);
      await take(page, "printing", theme, size.width);
    });
  }
}
