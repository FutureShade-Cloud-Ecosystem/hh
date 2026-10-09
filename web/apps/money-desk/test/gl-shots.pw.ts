// Screenshots of every ledger screen, at 390 and 1440 px, light and brand,
// walked on the stub the way the controller walks them. It runs only when
// GL_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   GL_SHOT_DIR=/some/folder npx playwright test gl-shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openApprovals } from "./gl-helpers.js";
import { join } from "node:path";

const dir = process.env.GL_SHOT_DIR ?? "";
test.skip(dir === "", "set GL_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

const FR1 = "2f6a1c00-0000-4000-9000-0000000000f1";
const J1_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a1";
const J2_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a2";

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-gl__${order}-${name}__${theme}-${width}.png`), fullPage: true });
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
      await expect(page.locator("[data-flow]")).toHaveCount(3);
      await brandOn();
      await take("01", "way-in");

      await page.goto("/");
      await page.evaluate(() => window.sessionStorage.clear());
      await openApprovals(page, request);
      await brandOn();
      await take("02", "approvals-queue");

      await page.locator(`[data-approval-id="${FR1}"]`).click();
      await expect(page.locator("[data-decide-panel]")).toContainText("Below floor quote request");
      await take("03", "decide-floor-request");

      await page.locator(`[data-approval-id="${J2_REQUEST}"]`).click();
      await expect(page.locator("[data-own-notice]")).toBeVisible();
      await take("04", "own-request-held");

      await page.locator(`[data-approval-id="${J1_REQUEST}"]`).click();
      await expect(page.getByRole("table", { name: "Journal lines" })).toBeVisible();
      await take("05", "decide-parked-journal");
      await page.locator("[data-decide-panel]").getByRole("button", { name: "Decide" }).click();
      await expect(page.locator("[data-notice]")).toContainText("posted");
      await take("06", "journal-approved");

      await page.locator("[data-tab-id='journals']").click();
      await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0006" }).click();
      await expect(page.locator("[data-journal-panel]")).toContainText("Inventory write-down");
      await take("07", "journals");

      await page.locator("[data-tab-id='trial']").click();
      await expect(page.locator("[data-trial]")).toContainText("In balance");
      await take("08", "trial-balance");

      await page.locator("[data-tab-id='approvals']").click();
      await page.route("**/api/v1/staff/approvals/*/decide", (route) =>
        route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }),
      );
      await page.locator(`[data-approval-id="${FR1}"]`).click();
      await page.locator("[data-decide-panel]").getByRole("button", { name: "Decide" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await take("09", "decision-refused-in-words");
    });
  }
}
