// UX-119c shots of the roles grid: collapsed, one shape opened, all opened, light and charcoal at 390, 1280, 1440.
// Runs only when DA_SHOT_DIR names a folder; also prints the page height. On main (before the change) the open
// controls do not exist, so the opened shots repeat the grid as it is.
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-119c-shots
import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");
test.use({ actionTimeout: 15_000 });

const SIZES = [{ width: 390, height: 844 }, { width: 1280, height: 800 }, { width: 1440, height: 900 }];
const THEMES = ["light", "charcoal"] as const;

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`UX-119c roles grid at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await page.goto("/");
      await page.locator("[data-flow]").first().click();
      await expect(page.locator('[data-test="staff-count"]')).toBeVisible({ timeout: 30_000 });
      if (theme === "charcoal") {
        await page.locator("hh-shell-header .account").click();
        await page.locator('[data-testid="theme-control"]').click();
        await page.locator("hh-shell-header .account").click();
      }
      if (size.width <= 900) {
        await page.locator('[data-action="toggle-surface-list"]').click();
      }
      await page.locator('.surface-link[data-surface="role-config"]').click();
      await expect(page.locator('[data-test="config-roles-count"]')).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      mkdirSync(dir, { recursive: true });
      const shot = async (name: string) => {
        await page.screenshot({ path: join(dir, `roles__${name}__${theme}-${size.width}.png`), fullPage: true, animations: "disabled" });
      };
      const height = async () => page.evaluate(() => document.documentElement.scrollHeight);
      await shot("collapsed");
      console.log(`HEIGHT ${theme} ${size.width} collapsed ${await height()}`);
      const one = page.locator('[data-test="config-shape-toggle-yard"]');
      if ((await one.count()) > 0) await one.click();
      await shot("one-opened");
      console.log(`HEIGHT ${theme} ${size.width} one-opened ${await height()}`);
      const all = page.locator('[data-test="config-toggle-all"]');
      if ((await all.count()) > 0) await all.click();
      await shot("all-opened");
      console.log(`HEIGHT ${theme} ${size.width} all-opened ${await height()}`);
    });
  }
}
