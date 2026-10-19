// Screenshots of every statements screen, at 390 and 1440 px, light and brand, walked on the stub the way the
// controller walks them. It runs only when FS_SHOT_DIR names a folder (so the suite's own run skips it), and
// writes there and nowhere else:
//
//   FS_SHOT_DIR=/some/folder npx playwright test finstmt-shots
//
// The brand theme is the design system's dark half; light is this app's default, one tap on the header's theme
// control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openStatements } from "./finstmt-helpers.js";
import { join } from "node:path";

const dir = process.env.FS_SHOT_DIR ?? "";
test.skip(dir === "", "set FS_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-statements__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      await openStatements(page, request);
      if (theme === "brand") {
        await page.locator("hh-shell-header button.account").click();
        await page.getByRole("button", { name: "Theme: Light" }).click();
      }
      await take("01", "income");
      await page.locator('[data-account="4000"] [data-action="fs-drill"]:visible').first().click();
      await expect(page.locator("[data-tie]")).toBeVisible();
      await take("02", "ledger-detail-ties");
      await page.locator('[data-tab-id="balance"]').click();
      await expect(page.locator("[data-statement]")).toContainText("Balanced");
      await take("03", "balance-sheet");
      await page.locator('[data-tab-id="trial"]').click();
      await expect(page.locator("[data-statement]")).toContainText("In balance");
      await take("04", "trial-balance");
      await page.locator('[data-tab-id="aging"]').click();
      await expect(page.locator('[data-total="grand"]')).toBeVisible();
      await take("05", "receivables-aging");
      await page.locator('[data-tab-id="packs"]').click();
      await expect(page.locator("[data-pack]")).toHaveCount(2);
      await take("06", "saved-packs");
      await page.locator('[data-tab-id="income"]').click();
      await page.getByLabel("Compare with the year before", { exact: true }).selectOption({ label: "With the year before" });
      await expect(page.locator("[data-change]").first()).toBeVisible();
      await take("07", "income-compared");
    });
  }
}
