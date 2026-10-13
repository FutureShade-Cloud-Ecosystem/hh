// Screenshots of every liens and compliance screen, at 390 and 1440 px, light and brand, walked on the stub the way the
// General Manager and the controller walk them. It runs only when LIEN_SHOT_DIR names a folder (so the suite's own run
// skips it), and writes there and nowhere else:
//
//   LIEN_SHOT_DIR=/some/folder npx playwright test lien-shots
//
// The brand theme is the design system's dark half; light is this app's default, one tap on the header's theme control
// moves between them.
import type { Page } from "@playwright/test";
import { join } from "node:path";
import { test, expect, openLiens, openCompliance, liensTab, complianceTab } from "./lien-helpers.js";

const dir = process.env.LIEN_SHOT_DIR ?? "";
test.skip(dir === "", "set LIEN_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-liens__${order}-${name}__${theme}-${width}.png`), fullPage: true });
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

      await openLiens(page, request, "general_manager");
      await brandOn();
      await take("01", "triage");
      await liensTab(page, "jobs");
      await page.locator("[data-row='job']:visible").first().getByRole("button", { name: "Open" }).click();
      await expect(page.locator("[data-job-panel]")).toBeVisible();
      await take("02", "jobs-open");
      await page.getByRole("button", { name: "New job" }).click();
      await page.locator("[data-job-form]").getByRole("button", { name: "Create job" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await take("03", "new-job-refused");
      await liensTab(page, "holdbacks");
      await take("04", "holdbacks");
      await page.locator("[data-row='aging'][data-job-no='GLS-2101']:visible").getByRole("button", { name: "Release" }).click();
      await expect(page.getByLabel("Co-signer")).toBeVisible();
      await take("05", "release-confirm");
      await page.getByLabel("Co-signer").selectOption({ label: "Payables and Receivables (former, demo)" });
      await page.getByRole("button", { name: "Confirm release" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await take("06", "release-refused-in-words");
      await page.getByLabel("Co-signer").selectOption({ label: "Financial Controller (demo)" });
      await page.getByRole("button", { name: "Confirm release" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Released");
      await take("07", "released");
      await liensTab(page, "documents");
      await page.getByLabel("Form", { exact: true }).selectOption({ label: "Conditional release" });
      await page.getByLabel("Owner", { exact: true }).fill("Summit Ridge Builders");
      await page.getByRole("button", { name: "Render draft" }).click();
      await expect(page.locator("[data-doc-view]")).toBeVisible();
      await take("08", "documents");

      await openCompliance(page, request, "controller");
      await brandOn();
      await take("09", "upcoming");
      await complianceTab(page, "calendar");
      await take("10", "calendar");
      await complianceTab(page, "review");
      await take("11", "review-status");
    });
  }
}
