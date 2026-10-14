// Screenshots of every dispatch screen, at 390 and 1440 px, light and brand, walked on the stub the
// way the role walks them. It runs only when DISPATCH_SHOT_DIR names a folder (so the suite's own
// run skips it), and writes there and nowhere else:
//
//   DISPATCH_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's default, one tap on the
// header's theme control moves between them.
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
  await page.screenshot({ path: join(dir, `dispatch-board__${order}-${name}__${theme}-${width}.png`), fullPage: true });
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
      await expect(page.locator("[data-flow]")).toHaveCount(2);
      await brandOn();
      await take("01", "way-in");

      await page.goto("/");
      await page.evaluate(() => window.sessionStorage.clear());
      await openBoard(page, request);
      await brandOn();
      await take("02", "load-board");

      await page.locator("[data-card='card.requested']").click();
      await take("03", "card-filter-requested");
      await page.getByRole("button", { name: "Show everything" }).click();

      await page.locator("[data-stop-id]").filter({ hasText: "ORD-0029" }).locator("select").first().selectOption({ index: 1 });
      await expect(page.locator("[data-notice]")).toBeVisible();
      await take("04", "load-put-on-a-truck");

      await page.getByRole("button", { name: "By status" }).click();
      await take("05", "by-status");
      await page.getByRole("button", { name: "By truck" }).click();

      await page.locator("[data-run-no='RUN-PLM-2']").getByRole("button", { name: "Dispatch" }).click({ force: true });
      await take("06", "dispatch-without-crew-refused");

      await page.locator("[data-stop-id]").filter({ hasText: "ORD-0014" }).getByRole("checkbox").click();
      await page.locator("[data-stop-id]").filter({ hasText: "ORD-0012" }).getByRole("checkbox").click();
      await take("07", "bulk-selected");
      await page.getByRole("button", { name: /Mark 2 loads loaded/ }).click();
      await expect(page.locator("[data-refusals]")).toBeVisible();
      await take("08", "bulk-loaded-one-held");

      await page.locator("[data-run-no='RUN-PLM-1']").getByRole("button", { name: "Reopen truck" }).click();
      await take("09", "reopen-sheet");

      await page.locator("[data-tab-id='day']").first().click();
      await expect(page.locator("[data-col]").first()).toBeVisible();
      await take("10", "day-board");
    });
  }
}
