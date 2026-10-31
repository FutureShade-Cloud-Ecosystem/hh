// UX-125 before/after shots: the pricing audit, the change log and the roles grid (Manager row).
// Set DA_SHOT_DIR to an output directory to run this file.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const OUT = process.env.DA_SHOT_DIR ?? "";
const SIZES = [{ width: 390, height: 844 }, { width: 1280, height: 800 }, { width: 1440, height: 900 }];

for (const theme of ["light", "charcoal"] as const) {
  for (const size of SIZES) {
    test(`UX-125 shots ${theme} ${size.width}`, async ({ page }) => {
      test.skip(OUT === "", "set DA_SHOT_DIR");
      test.setTimeout(120_000);
      mkdirSync(OUT, { recursive: true });
      await page.setViewportSize(size);
      await page.request.post("/_stub/reset");
      await page.goto("/");
      await page.locator("[data-flow]").first().click();
      await expect(page.locator('[data-test="staff-count"]')).toBeVisible({ timeout: 30_000 });
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }
      const open = async (surface: string) => {
        if (size.width <= 900) {
          const list = page.locator(".surface-list");
          if (!(await list.isVisible())) await page.locator('[data-action="toggle-surface-list"]').click();
        }
        await page.locator(`.surface-link[data-surface="${surface}"]`).click();
        await page.waitForLoadState("networkidle");
        await page.waitForTimeout(300);
      };
      const tab = async (id: string, title: string, settle: string) => {
        if (size.width <= 900) await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
        else await page.locator(`.pr-tab[data-tab="${id}"]`).click();
        await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
        await page.waitForLoadState("networkidle");
        await page.waitForTimeout(300);
      };
      const shot = async (name: string) =>
        page.screenshot({ path: join(OUT, `${name}__${theme}-${size.width}.png`), fullPage: true, animations: "disabled" });
      await open("pricing");
      await tab("audit", "Pricing audit", '[data-test="pr-audit-count"]');
      await shot("pricing-audit");
      await tab("changelog", "Change log", '[data-test="pr-log-count"], [data-test="pr-log-empty"]');
      await shot("change-log");
      await open("role-config");
      await expect(page.locator('[data-test="config-roles-count"]')).toBeVisible({ timeout: 30_000 });
      await shot("roles-grid");
    });
  }
}
