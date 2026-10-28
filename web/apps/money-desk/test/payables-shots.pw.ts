// The Payables board photographed in its three states, in light and in charcoal, at 390, 1280
// and 1440 wide. The desk's own Receivables aging is photographed beside it at the same sizes and
// the same themes, for the side by side review.
//
// It runs only when MD_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test payables-shots
//
// Charcoal is the design system's dark half. The theme control rides the band's account menu,
// and its word names the theme it is in, so one tap moves from the light half to the dark one.
import type { Page } from "@playwright/test";
import { test, expect, openDesk, resetStub } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function shot(page: Page, theme: string, width: number, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${name}-${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the Payables board at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const charcoalOn = async (on: Page = page) => {
        if (theme === "charcoal") {
          await on.locator("hh-shell-header button.account").click();
          await on.getByRole("button", { name: "Theme: Light" }).click();
        }
      };

      await openDesk(page, request);
      await charcoalOn();
      // The reference: the desk's own Receivables aging, the board's sibling.
      await shot(page, theme, size.width, "reference");

      // The board: the open AP by payee.
      await page.getByRole("button", { name: "Payables", exact: true }).click();
      await expect(page.locator("[data-payables-aging]")).toBeVisible();
      await shot(page, theme, size.width, "aging");

      // A payee's open items: an open bill and the landed cost line marked as one.
      await page.locator("[data-payee-row]", { hasText: "Northwind Freight Lines (demo)" }).getByRole("button").click();
      await expect(page.locator("[data-payables-lines]")).toBeVisible();
      await shot(page, theme, size.width, "vendor");

      // The empty state: the board reads the books on a day nothing is owed on.
      await request.post("/_stub/scenario", { data: { name: "empty" } });
      await page.getByRole("button", { name: "‹ Payables" }).click();
      await expect(page.locator("[data-payables-aging]")).toBeVisible();
      await page.getByRole("button", { name: "Payables", exact: true }).click();
      await expect(page.locator("[data-payables-empty]")).toBeVisible();
      await shot(page, theme, size.width, "empty");
      await resetStub(request);
    });
  }
}
