// UX-120 before/after screenshot capture for Dealer Admin.
// Set HH_UX120_SHOT_DIR to an output directory to run this file.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const OUT_DIR = process.env.HH_UX120_SHOT_DIR ?? "";
const RUN = OUT_DIR !== "";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function goCharcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header .account").click();
  await page.locator('[data-testid="theme-control"]').click();
  await page.locator("hh-shell-header .account").click();
}

async function expectVisible(page: Page, settle: string): Promise<void> {
  const visible = page.locator(settle.split(",").map((s) => `${s.trim()}:visible`).join(", ")).first();
  await expect(visible).toBeVisible({ timeout: 30_000 });
}

async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    const list = page.locator(".surface-list");
    if (!(await list.isVisible())) {
      await page.locator('[data-action="toggle-surface-list"]').click();
    }
    await expect(list).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expectVisible(page, settle);
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(250);
}

async function openPricingView(page: Page, id: string, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab[data-tab="${id}"]`).click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(250);
}

async function snap(page: Page, name: string): Promise<void> {
  await page.screenshot({ path: join(OUT_DIR, `${name}.png`), fullPage: false });
}

test.describe("UX-120 screenshots", () => {
  test.skip(!RUN, "set HH_UX120_SHOT_DIR to capture UX-120 screenshots");

  for (const theme of ["light", "charcoal"] as const) {
    for (const vp of [
      { label: "1440", width: 1440, height: 900 },
      { label: "390", width: 390, height: 844 },
    ] as const) {
      test(`capture UX-120 views in ${theme} at ${vp.label}`, async ({ page }) => {
        mkdirSync(OUT_DIR, { recursive: true });
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await page.request.post("/_stub/reset");
        await signIn(page);
        if (theme === "charcoal") await goCharcoal(page);

        const suffix = `${theme}-${vp.label}`;

        // 1. Staff
        await openSurface(page, "staff", '[data-test="staff-count"]');
        await snap(page, `staff__register__${suffix}`);

        // 2. Roles editor (top and workspace section)
        await openSurface(page, "role-config", '[data-test="config-roles-count"]');
        await page.locator('[data-test="config-cell-counter-till-yard"]').click();
        await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
        await page.locator('[data-test="config-editor"]').scrollIntoViewIfNeeded();
        await page.waitForTimeout(150);
        await snap(page, `roles__editor__${suffix}`);
        await page.locator('[data-field="configws-split"]').scrollIntoViewIfNeeded();
        await page.waitForTimeout(150);
        await snap(page, `roles__editor-workspace__${suffix}`);

        // 3. Quantity breaks (list) and 4. New schedule (editor)
        await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
        await openPricingView(page, "breaks", "Quantity breaks", '[data-test="pr-breaks-count"]');
        await snap(page, `pricing__breaks__${suffix}`);

        await page.locator('[data-action="pr-break-edit"]').first().click();
        await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
        await page.locator('[data-test="pr-break-editor"]').scrollIntoViewIfNeeded();
        await page.waitForTimeout(150);
        await snap(page, `pricing__new-schedule__${suffix}`);

        // 5. Rebates (programs and accrual ledger)
        await openPricingView(page, "rebates", "Rebates", '[data-test="pr-rebate-count"]');
        await snap(page, `pricing__rebates__${suffix}`);
        await page.locator('[data-test="pr-ledger-total"]').scrollIntoViewIfNeeded();
        await page.waitForTimeout(150);
        await snap(page, `pricing__rebates-ledger__${suffix}`);

        // 6. Pricing audit (Pricing log card)
        await openPricingView(page, "audit", "Pricing audit", '[data-test="pr-audit-count"]');
        if (vp.width <= 900) {
          await page.locator(".tablewrap table tbody tr").last().scrollIntoViewIfNeeded();
          await page.waitForTimeout(150);
        }
        await snap(page, `pricing__audit__${suffix}`);

        // 7. Change log
        await openPricingView(page, "changelog", "Change log", '[data-test="pr-log-count"]');
        await snap(page, `pricing__changelog__${suffix}`);
      });
    }
  }
});
