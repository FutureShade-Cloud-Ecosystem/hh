// Screenshots of every tax screen, at 390 and 1440 px, light and brand, walked on the stub the way the controller
// walks them. It runs only when TAX_SHOT_DIR names a folder (so the suite's own run skips it), and writes there
// and nowhere else:
//
//   TAX_SHOT_DIR=/some/folder npx playwright test tax-shots
import type { Page } from "@playwright/test";
import { test, expect, openTax, tab } from "./tax-helpers.js";
import { join } from "node:path";

const dir = process.env.TAX_SHOT_DIR ?? "";
test.skip(dir === "", "set TAX_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-tax__${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openTax(page, request);
      if (theme === "brand") {
        await page.locator("hh-shell-header button.account").click();
        await page.getByRole("button", { name: "Theme: Light" }).click();
      }
      const take = (name: string) => shot(page, theme, size.width, name);
      await take("rates");
      await page.getByRole("textbox", { name: "Rate, percent" }).fill("8.25");
      await page.getByRole("button", { name: "Add the rate", exact: true }).click();
      await expect(page.locator("[data-confirm='rate']")).toBeVisible();
      await take("rates-confirm");
      await tab(page, "profiles");
      await take("profiles");
      await tab(page, "certificates");
      await page.locator("[data-evidence-id]:visible, [data-evidence-card]:visible").last().getByRole("button", { name: "Open the record" }).click();
      await page.getByRole("button", { name: "Check the certificate" }).click();
      await expect(page.locator("[data-check-result]")).toBeVisible();
      await take("certificates");
      await tab(page, "settings");
      await take("settings");
      await tab(page, "checks");
      await page.locator("[data-field='tx-r-month']").selectOption({ index: new Date().getMonth() - 1 >= 0 ? new Date().getMonth() - 1 : 11 });
      await page.getByRole("button", { name: "Reconcile", exact: true }).click();
      await expect(page.locator("[data-recon-result]")).toBeVisible();
      await take("checks");
    });
  }
}
