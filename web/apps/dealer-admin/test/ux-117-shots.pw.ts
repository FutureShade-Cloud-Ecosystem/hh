// Dealer Admin's UX-117 audit screenshots: each form touched by UX-117 at
// 390 and 1440 px, light and charcoal, full page with animations off and
// the page settled. Runs only when DA_SHOT_DIR names a folder:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-117-shots
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
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

async function take(page: Page, name: string, theme: string, width: number): Promise<void> {
  await page.screenshot({
    path: join(dir, `${name}__${theme}-${width}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`UX-117 forms at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(180_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }

      await openSection(page, "pricing", '[data-test="pr-margin-count"]');

      // 1. Rules editor
      await page.locator('.pr-tab[data-tab="rules"]').click();
      await expect(page.locator('[data-test="pr-rules-count"]')).toBeVisible();
      await page.locator('[data-action="pr-rule-edit"]').first().click();
      await expect(page.locator('[data-test="pr-rule-editor"]')).toBeVisible();
      await take(page, "pricing__rules-editor", theme, size.width);

      // 2. Customer prices
      await page.locator('.pr-tab[data-tab="customers"]').click();
      await expect(page.locator('[data-test="pr-cust-empty"]')).toBeVisible();
      await take(page, "pricing__customers", theme, size.width);

      // 3. Quantity breaks editor
      await page.locator('.pr-tab[data-tab="breaks"]').click();
      await expect(page.locator('[data-test="pr-breaks-count"]')).toBeVisible();
      await page.locator('[data-action="pr-break-edit"]').first().click();
      await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
      await take(page, "pricing__breaks-editor", theme, size.width);

      // 4. Promotions & clearance editors
      await page.locator('.pr-tab[data-tab="promos"]').click();
      await expect(page.locator('[data-test="pr-promo-count"]')).toBeVisible();
      await expect(page.locator('[data-test="pr-clear-count"]')).toBeVisible();
      await page.locator('[data-action="pr-promo-edit"]').first().click();
      await page.locator('[data-action="pr-clear-edit"]').first().click();
      await expect(page.locator('[data-test="pr-promo-editor"]')).toBeVisible();
      await expect(page.locator('[data-test="pr-clear-editor"]')).toBeVisible();
      await take(page, "pricing__promos-editor", theme, size.width);

      // 5. Rebates editor & accrual form
      await page.locator('.pr-tab[data-tab="rebates"]').click();
      await expect(page.locator('[data-test="pr-rebate-count"]')).toBeVisible();
      await expect(page.locator('[data-test="pr-ledger-total"]')).toBeVisible();
      await page.locator('[data-action="pr-rebate-edit"]').first().click();
      await expect(page.locator('[data-test="pr-rebate-editor"]')).toBeVisible();
      await take(page, "pricing__rebates", theme, size.width);

      // 6. Units of measure probe & editor
      await page.locator('.pr-tab[data-tab="uom"]').click();
      await expect(page.locator('[data-test="pr-uom-count"]')).toBeVisible();
      await page.locator('[data-action="pr-uom-edit"]').first().click();
      await expect(page.locator('[data-test="pr-uom-editor"]')).toBeVisible();
      await take(page, "pricing__uom", theme, size.width);

      // 7. Batches create form
      await page.locator('.pr-tab[data-tab="batches"]').click();
      await expect(page.locator('[data-test="pr-batches-count"]')).toBeVisible();
      await expect(page.locator('[data-test="pr-profiles-count"]')).toBeVisible();
      await take(page, "pricing__batches", theme, size.width);

      // 8. Low margin probe
      await page.locator('.pr-tab[data-tab="lowmargin"]').click();
      await expect(page.locator('[data-test="pr-lm-count"]')).toBeVisible();
      await take(page, "pricing__lowmargin", theme, size.width);

      // 9. Change log filters
      await page.locator('.pr-tab[data-tab="changelog"]').click();
      await expect(page.locator('[data-test="pr-log-count"]')).toBeVisible();
      await take(page, "pricing__changelog", theme, size.width);

      // 10. Audit filters
      await openSection(page, "audit", '[data-test="audit-count"]');
      await take(page, "audit__filters", theme, size.width);

      // 11. Exports filters
      await openSection(page, "exports", '[data-action="wb-download"]');
      await take(page, "exports__filters", theme, size.width);

      // 12. Role configuration editor
      await openSection(page, "role-config", '[data-test="config-roles-count"]');
      await page.locator('[data-test="config-cell-counter-till-yard"]').click();
      await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
      await take(page, "roles__editor", theme, size.width);
    });
  }
}
