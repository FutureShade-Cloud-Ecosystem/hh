// Screenshots of the touched screen: the Money Desk ledger's Periods tab with the Controller's
// open year control and the late posted list, and the review the open year opens. It runs only when
// GL_SHOT_DIR names a folder (so the suite's own run skips it), and writes there and nowhere else:
//
//   GL_SHOT_DIR=/some/folder npx playwright test fw1-01c-shots
//
// The brand theme is the design system's dark half, the charcoal half; light is this app's default and
// one tap on the header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openApprovals } from "./gl-helpers.js";
import { join } from "node:path";

const dir = process.env.GL_SHOT_DIR ?? "";
test.skip(dir === "", "set GL_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-gl__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the periods tab at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") {
          await page.locator("hh-shell-header button.account").click();
          await page.getByRole("button", { name: "Theme: Light" }).click();
        }
      };

      await openApprovals(page, request);
      await brandOn();
      await page.locator("[data-tab-id='periods']").click();
      await expect(page.getByRole("table", { name: "Late posted journals" })).toBeVisible();
      await take("01", "periods");

      await page.getByRole("button", { name: "Open fiscal year 2027" }).first().click();
      await expect(page.locator("[data-confirm]")).toBeVisible();
      await take("02", "open-year-review");
    });
  }
}
