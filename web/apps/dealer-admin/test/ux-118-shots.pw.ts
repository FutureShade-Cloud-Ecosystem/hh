// Dealer Admin's UX-118 audit screenshots: the desk-width tables and dealer
// words views at 390, 1280, and 1440 px, light and charcoal, full page with
// animations off and the page settled. Runs only when DA_SHOT_DIR names a
// folder:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-118-shots
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");
test.use({ actionTimeout: 15_000 });

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function openDealerAdmin(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator('[data-test="staff-count"]')).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function openSection(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    const list = page.locator(".surface-list");
    if (!(await list.isVisible())) {
      await page.locator('[data-action="toggle-surface-list"]').click();
      await expect(list).toBeVisible();
    }
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function openPricingTab(page: Page, tabId: string, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-test="pr-tab-select"]').selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab[data-tab="${tabId}"]`).click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function take(page: Page, name: string, theme: string, width: number, fullPage = true): Promise<void> {
  mkdirSync(dir, { recursive: true });
  await page.screenshot({
    path: join(dir, `${name}__${theme}-${width}.png`),
    fullPage,
    animations: "disabled",
  });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`UX-118 desk widths and dealer words at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(180_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }

      // 1. Staff register
      await take(page, "staff__register", theme, size.width);

      // 2. Roles grid and editor
      await openSection(page, "role-config", '[data-test="config-roles-count"]');
      await page.locator('[data-action="open-config-editor"]').first().click();
      await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
      await take(page, "roles__grid", theme, size.width);
      await page.locator('[data-test="config-editor"]').scrollIntoViewIfNeeded();
      await take(page, "roles__editor", theme, size.width, false);

      // 3. Pricing: Quantity breaks editor
      await openSection(page, "pricing", '[data-test="pr-margin-count"]');
      await openPricingTab(page, "breaks", "Quantity breaks", '[data-test="pr-breaks-count"]');
      await page.locator('[data-action="pr-break-edit"]').first().click();
      await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
      await take(page, "pricing__breaks-editor", theme, size.width);

      // 4. Pricing: Rebates accrual
      await openPricingTab(page, "rebates", "Rebates", '[data-test="pr-ledger-total"]');
      await expect(page.locator('[data-test="pr-rebate-count"]')).toBeVisible();
      await take(page, "pricing__rebates-accrual", theme, size.width);

      // 5. Pricing: Batches & imports
      await openPricingTab(page, "batches", "Batches & imports", '[data-test="pr-profiles-count"]');
      await expect(page.locator('[data-test="pr-batches-count"]')).toBeVisible();
      await take(page, "pricing__batches", theme, size.width);

      // 6. Pricing: Audit log
      await openPricingTab(page, "audit", "Pricing audit", '[data-test="pr-audit-count"]');
      await take(page, "pricing__audit", theme, size.width);
    });
  }
}
