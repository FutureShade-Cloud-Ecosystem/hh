// Screenshots of every receivables screen, at 390 and 1440 px, light and brand,
// walked on the stub the way the role walks them. It runs only when
// MD_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openDesk } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-ar__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
      };

      await request.post("/_stub/reset");
      await page.goto("/");
      await expect(page.locator("[data-flow]")).toHaveCount(1);
      await brandOn();
      await take("01", "way-in");

      await page.goto("/");
      await page.evaluate(() => window.sessionStorage.clear());
      await openDesk(page, request);
      await brandOn();
      await take("02", "landing-aging");

      await page.locator("[data-tab='bounced']").click();
      await page.locator("[data-bounced-row]").getByRole("button", { name: "Work it" }).click();
      await expect(page.locator("[data-bounced-work]")).toBeVisible();
      await take("03", "bounced-queue-worked");

      await page.locator("[data-tab='holds']").click();
      await page.locator("[data-field='release-reason']").fill("Paid in full");
      await page.getByRole("button", { name: "Release hold" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await take("04", "hold-release-refused");

      await page.locator("[data-tab='aging']").click();
      await page.locator("[data-aging-row]", { hasText: "Granite Edge Landscaping" }).getByRole("button", { name: "Open account" }).click();
      await expect(page.locator("[data-statement]")).toBeVisible();
      await take("05", "account-invoices");

      await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Open invoice" }).click();
      await expect(page.locator("[data-invoice-panel]")).toBeVisible();
      await take("06", "invoice-forms");

      await page.locator("[data-field='pay-amount']").fill("100.00");
      await page.locator("[data-field='pay-reference']").fill("Cheque 1042");
      await page.getByRole("button", { name: "Apply payment" }).click();
      await expect(page.locator("[data-notice]")).toContainText("applied");
      await take("07", "payment-applied");

      await page.getByRole("button", { name: "Run a statement" }).click();
      await expect(page.locator("[data-statement]")).toContainText("Closing balance");
      await take("08", "statement");
    });
  }
}
