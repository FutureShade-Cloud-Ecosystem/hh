// Screenshots of every procurement screen, at 390 and 1440 px, light and brand,
// walked on the stub the way the role walks them. It runs only when
// BS_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   BS_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openHome } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.BS_SHOT_DIR ?? "";
test.skip(dir === "", "set BS_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `buying-stock-procurement__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") await page.getByRole("button", { name: "Theme: Light" }).click();
      };

      await request.post("/_stub/reset");
      await page.goto("/");
      await expect(page.locator("[data-flow]")).toHaveCount(1);
      await brandOn();
      await take("01", "way-in");

      await page.goto("/");
      await page.evaluate(() => window.sessionStorage.clear());
      await openHome(page, request);
      await brandOn();
      await take("02", "home-k08-gap");

      await take("03", "gap-row-picked-on-landing");

      await page.getByRole("button", { name: "Draft PO (1)" }).click();
      await expect(page.locator("[data-draft]")).toBeVisible();
      await take("04", "draft-from-shortfall");

      await page.locator("[data-field='expected']").fill("2031-03-04");
      await page.locator("[data-method='DELIVERY']").click();
      await page.getByRole("button", { name: "Save draft" }).click();
      await expect(page.locator("[data-open-draft]")).toBeVisible();
      await take("04b", "drafted-not-placed-row-stays");

      await page.locator("[data-open-draft]").click();
      await page.getByRole("button", { name: "Place order" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Placed: PO-TRT-142");
      await take("05", "placed");

      await page.locator("[data-tab-id='incoming']").first().click();
      await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-142" })).toBeVisible();
      await take("06", "incoming");

      await page.locator("[data-tab-id='orders']").first().click();
      await request.post("/_stub/wall");
      await page.locator("[data-shortfall-row]").first().getByRole("checkbox").click();
      await page.getByRole("button", { name: "Draft PO (1)" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await take("07", "ordering-refused-in-words");
    });
  }
}
