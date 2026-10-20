// GAPS-r2 G3: the page starts at x 0. The browser's default 8 px body margin
// showed as a grey strip at the left and top of every capture.
import { test, expect } from "@playwright/test";

test("the body carries no default margin, so the page starts at x 0", async ({ page }) => {
  await page.goto("/");
  const host = page.locator("body > *").first();
  await expect(host).toBeVisible();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).margin)).toBe("0px");
  await expect.poll(async () => (await host.boundingBox())?.x).toBe(0);
});
