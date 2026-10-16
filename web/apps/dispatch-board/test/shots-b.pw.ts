// Screenshots of every part B screen, at 390 and 1440 px, light and brand, walked on the stub. It runs
// only when DISPATCH_SHOT_DIR names a folder, and writes there and nowhere else:
//
//   DISPATCH_SHOT_DIR=/some/folder npx playwright test shots-b
import type { Page } from "@playwright/test";
import { test, expect, openBoard } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.DISPATCH_SHOT_DIR ?? "";
test.skip(dir === "", "set DISPATCH_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `dispatch-board-b__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`part B walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const tab = (id: string) => page.locator(`[data-tab-id='${id}']`).first().click();
      await openBoard(page, request);
      if (theme === "brand") await page.getByRole("button", { name: "Theme: Light" }).click();

      await tab("schedule");
      await expect(page.locator("[data-schedule]")).toBeVisible();
      await take("01", "schedule");
      await page.locator("[data-schedule] [data-field='order']").fill("ORD-0041");
      await page.getByRole("button", { name: "Schedule delivery" }).click();
      await expect(page.locator("[data-schedule-error]")).toBeVisible();
      await take("02", "schedule-refused");

      await tab("fleet");
      await expect(page.locator("[data-trucks]")).toBeVisible();
      await take("03", "fleet");
      await page.locator("[data-truck-code='Truck 01']").getByRole("button", { name: "Change" }).click();
      await take("04", "fleet-change-truck");
      await page.locator("[data-sheet]").getByRole("button", { name: "Cancel" }).click();

      await tab("willcall");
      await page.locator("[data-willcall] [data-field='wc-order']").fill("ORD-0044");
      await page.locator("[data-willcall]").getByRole("button", { name: "Print a pickup code" }).click();
      await expect(page.locator("[data-tag]")).toBeVisible();
      await take("05", "willcall-tag");

      await tab("settings");
      await expect(page.locator("[data-rules]")).toBeVisible();
      await page.getByRole("button", { name: "Change the rules" }).click();
      await page.locator("[data-sheet] [data-field='prompt']").fill("24");
      await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
      await expect(page.locator("[data-history-row]").first()).toBeVisible();
      await take("06", "settings");

      await tab("load");
      await expect(page.locator("[data-card]").first()).toBeVisible();
      await page.locator("li.load", { hasText: "ORD-0012" }).getByRole("button", { name: "Scan trail" }).click();
      await expect(page.locator("[data-trail-load]").first()).toBeVisible();
      await take("07", "scan-trail");
      await page.locator("[data-sheet='scan-trail']").screenshot({ path: join(dir, `dispatch-board-b__08-scan-trail-sheet__${theme}-${size.width}.png`) });
    });
  }
}
