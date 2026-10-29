// Screenshots of the touched screen: the Money Desk ledger's Journals tab, the register with its
// posted date range set, after Show more has appended the older page, and with a range that matches
// nothing. It runs only when GL_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   GL_SHOT_DIR=/some/folder npx playwright test journal-reach-shots
//
// The register's own filter row is the reference: the range row carries the same spacing and tokens,
// in light and in brand, the design system's charcoal half. Light is this app's default and one tap
// on the header's theme control moves between them.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, controllerHandoff, resetStub } from "./gl-helpers.js";
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
    test(`the register's reach at ${size.width} px, ${theme}`, async ({ page, request }: { page: Page; request: APIRequestContext }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") {
          await page.locator("hh-shell-header button.account").click();
          await page.getByRole("button", { name: "Theme: Light" }).click();
        }
      };

      await resetStub(request);
      const planted = await request.post("/api/v1/gl/journals/_stub/planter", {
        data: { count: 130 },
        headers: { Authorization: "Bearer the-planter-is-a-stub-control" },
      });
      if (!planted.ok()) throw new Error("the stub refused the older journals");
      await page.addInitScript(controllerHandoff);
      await page.goto("/");
      await expect(page.locator("[data-approval-id]")).toHaveCount(5);
      await brandOn();
      await page.locator("[data-tab-id='journals']").click();
      const register = page.getByRole("table", { name: "Journal register" });
      await expect(register).toBeVisible();

      // The range set: the older year in the register, the window said in words beside the fields.
      await page.locator('[data-field="journal-from"]').fill("2025-01-01");
      await page.locator('[data-field="journal-to"]').fill("2025-12-31");
      await page.getByRole("button", { name: "Apply dates" }).click();
      await expect(page.locator("[data-range-words]")).toContainText("2025-01-01 through 2025-12-31");
      await take("01", "range-set");

      // After show more: the range off again, the first hundred, then the older page appended.
      await page.locator('[data-field="journal-from"]').fill("");
      await page.locator('[data-field="journal-to"]').fill("");
      await page.getByRole("button", { name: "Apply dates" }).click();
      await expect(page.locator("[data-show-more]")).toBeVisible();
      await page.locator("[data-show-more]").click();
      await expect(register.getByRole("button", { name: "JE-0101" })).toBeVisible();
      await expect(page.locator("[data-show-more]")).toHaveCount(0);
      await take("02", "show-more");

      // A range that matches nothing: the register says so in words.
      await page.locator('[data-field="journal-from"]').fill("2024-01-01");
      await page.locator('[data-field="journal-to"]').fill("2024-01-31");
      await page.getByRole("button", { name: "Apply dates" }).click();
      await expect(page.locator(".gl-empty")).toContainText("No journals here.");
      await take("03", "range-empty");
    });
  }
}
