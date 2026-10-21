// Screenshots of the load board's time view at 390 and 1440 px, light and charcoal. Runs only when
// DISPATCH_SHOT_DIR names a folder, and writes there and nowhere else.
import { test, expect, openBoard } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.DISPATCH_SHOT_DIR ?? "";
test.skip(dir === "", "set DISPATCH_SHOT_DIR to take the screenshots");

for (const width of [1440, 390]) {
  for (const theme of ["light", "brand"] as const) {
    test(`the time view at ${width} px, ${theme}`, async ({ page, request }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await openBoard(page, request);
      if (theme === "brand") await page.getByRole("button", { name: "Theme: Light" }).click();
      await expect(page.locator("[data-time-axis] .has-stop").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.locator("[data-time-axis]").scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(dir, `time-axis__${theme === "brand" ? "charcoal" : "light"}-${width}.png`), fullPage: true });
    });
  }
}
