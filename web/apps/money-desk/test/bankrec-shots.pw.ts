// Screenshots of every bank reconciliation screen, at 390 and 1440 px, light and brand, walked on the stub the way
// the controller walks them. It runs only when BANKREC_SHOT_DIR names a folder (so the suite's own run skips it),
// and writes there and nowhere else:
//
//   BANKREC_SHOT_DIR=/some/folder npx playwright test bankrec-shots
//
// The brand theme is the design system's dark half; light is this app's default, one tap on the header's theme
// control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openBank, tab, monthDay, WIRE_PAYMENT, FEE_JOURNAL, CLOSING, OPERATING } from "./bankrec-helpers.js";
import { join } from "node:path";

const dir = process.env.BANKREC_SHOT_DIR ?? "";
test.skip(dir === "", "set BANKREC_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-bankrec__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      await openBank(page, request);
      if (theme === "brand") await page.getByRole("button", { name: "Theme: Light" }).click();
      await take("01", "accounts");

      await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
      await page.getByRole("button", { name: "Open month" }).click();
      await expect(page.locator("[data-notice]")).toContainText("is open");
      await take("02", "month-open-held");

      await tab(page, "import");
      await page.getByRole("textbox", { name: "Statement rows" }).fill(`Date,Description,Amount,Reference\n${monthDay(27)},YARD SUPPLY CARD PAYMENT,"(1,200.50)",REF-1\n`);
      await page.getByRole("button", { name: "Import statement" }).click();
      await expect(page.locator("[data-import-result]")).toBeVisible();
      await take("03", "import-done");
      await page.getByRole("textbox", { name: "Statement rows" }).fill("Date,Description,Reference\nx,y,z\n");
      await page.getByRole("button", { name: "Import statement" }).click();
      await expect(page.locator("[data-import-failed]")).toBeVisible();
      await take("04", "import-failed-in-words");

      await tab(page, "match");
      await take("05", "match-unmatched");
      await page.getByRole("button", { name: "Run auto match" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Auto match paired");
      await take("06", "auto-matched");
      await page.locator("[data-line-id], [data-line-card]").filter({ hasText: "WIRE FROM CONTRACTOR" }).locator("visible=true").first().getByRole("button").click();
      await expect(page.locator("[data-line-panel]")).toBeVisible();
      await take("07", "match-by-hand");
      await page.locator("[data-line-panel]").getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
      await page.locator("[data-line-panel]").getByRole("button", { name: "Match line" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Matched.");
      await page.getByRole("button", { name: "Matched", exact: true }).click();
      await page.locator("[data-line-id], [data-line-card]").filter({ hasText: "CARD SETTLEMENT BATCH 0143" }).locator("visible=true").first().getByRole("button").click();
      await expect(page.locator("[data-history]")).toBeVisible();
      await take("08", "matched-line-history");

      await tab(page, "accounts");
      await page.locator("[data-recon-id], [data-recon-card]").locator("visible=true").first().getByRole("button", { name: "Refresh figures" }).click();
      await expect(page.locator("[data-notice]")).toContainText("is open");
      await take("09", "month-refreshed");
      await page.locator("[data-recon-id], [data-recon-card]").locator("visible=true").first().getByRole("button", { name: "Sign off" }).click();
      await page.getByRole("button", { name: "Confirm sign off" }).click();
      await expect(page.locator("[data-error]")).toContainText("Not signed off");
      await take("10", "sign-off-refused-in-words");

      await tab(page, "rules");
      await expect(page.locator("[data-rule-id], [data-rule-card]").locator("visible=true").first()).toBeVisible();
      await take("11", "rules");
      void FEE_JOURNAL;
      void OPERATING;
    });
  }
}
