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

async function openPricingTab(page: Page, tabId: string, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-test="pr-tab-select"]').selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab[data-tab="${tabId}"]`).click();
  }
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
      await openPricingTab(page, "rules", "Rules", '[data-test="pr-rules-count"]');
      await page.locator('[data-action="pr-rule-edit"]').first().click();
      await expect(page.locator('[data-test="pr-rule-editor"]')).toBeVisible();
      await take(page, "pricing__rules-editor", theme, size.width);

      // 2. Customer prices
      await openPricingTab(page, "customers", "Customer prices", '[data-test="pr-cust-empty"]');
      await take(page, "pricing__customers", theme, size.width);

      // 3. Quantity breaks editor
      await openPricingTab(page, "breaks", "Quantity breaks", '[data-test="pr-breaks-count"]');
      await page.locator('[data-action="pr-break-edit"]').first().click();
      await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
      await take(page, "pricing__breaks-editor", theme, size.width);

      // 4. Promotions & clearance editors
      await openPricingTab(page, "promos", "Promotions & clearance", '[data-test="pr-clear-count"]');
      await expect(page.locator('[data-test="pr-promo-count"]')).toBeVisible();
      await page.locator('[data-action="pr-promo-edit"]').first().click();
      await page.locator('[data-action="pr-clear-edit"]').first().click();
      await expect(page.locator('[data-test="pr-promo-editor"]')).toBeVisible();
      await expect(page.locator('[data-test="pr-clear-editor"]')).toBeVisible();
      await take(page, "pricing__promos-editor", theme, size.width);

      // 5. Rebates editor & accrual form
      await openPricingTab(page, "rebates", "Rebates", '[data-test="pr-ledger-total"]');
      await expect(page.locator('[data-test="pr-rebate-count"]')).toBeVisible();
      await page.locator('[data-action="pr-rebate-edit"]').first().click();
      await expect(page.locator('[data-test="pr-rebate-editor"]')).toBeVisible();
      await take(page, "pricing__rebates", theme, size.width);

      // 6. Units of measure probe & editor
      await openPricingTab(page, "uom", "Units of measure", '[data-test="pr-uom-count"]');
      await page.locator('[data-action="pr-uom-edit"]').first().click();
      await expect(page.locator('[data-test="pr-uom-editor"]')).toBeVisible();
      await take(page, "pricing__uom", theme, size.width);

      // 7. Batches create form
      await openPricingTab(page, "batches", "Batches & imports", '[data-test="pr-profiles-count"]');
      await expect(page.locator('[data-test="pr-batches-count"]')).toBeVisible();
      await page.locator('[data-action="pr-batch-new"]').click();
      await expect(page.locator('[data-test="pr-batch-create"]')).toBeVisible();
      await take(page, "pricing__batches", theme, size.width);

      // 8. Low margin probe
      await openPricingTab(page, "lowmargin", "Low margin", '[data-test="pr-lm-count"]');
      await take(page, "pricing__lowmargin", theme, size.width);

      // 9. Change log filters
      await openPricingTab(page, "changelog", "Change log", '[data-test="pr-log-count"]');
      await take(page, "pricing__changelog", theme, size.width);

      // 10. Audit filters
      await openSection(page, "audit", '[data-test="audit-count"]');
      await take(page, "audit__filters", theme, size.width);

      // 11. Exports filters
      await openSection(page, "exports", '[data-action="wb-download"]');
      await take(page, "exports__filters", theme, size.width);

      // 12. Role configuration editor
      await openSection(page, "role-config", '[data-test="config-roles-count"]');
      await openShape(page, "yard");
      await page.locator('[data-test="config-cell-counter-till-yard"]').click();
      await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
      await take(page, "roles__editor", theme, size.width);
    });
  }
}

// UX-119c: a job shape with nothing curated is one line until asked; open it before clicking one of its tiles.
async function openShape(page: import("@playwright/test").Page, profile: string): Promise<void> {
  const toggle = page.locator(`[data-test="config-shape-toggle-${profile}"]`);
  if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
}
