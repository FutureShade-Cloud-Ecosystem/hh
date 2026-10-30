// The on account payment photographed whole, in light and in charcoal, at 390 and 1280 wide:
// a cheque larger than the one invoice it is picked against, so the on account sentence, the
// unapplied pill and the hold strip all show at once. The 1280 shot is full page, through Record
// payment and Cancel, which the first round of these shots cut off at the Applied row.
//
// It runs only when MD_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test on-account-shots
//
// Charcoal is the design system's dark half. The theme control rides the band's account menu,
// and its word names the theme it is in, so one tap moves from the light half to the dark one.
import type { Page } from "@playwright/test";
import { test, expect, openDesk } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
];
const THEMES = ["light", "charcoal"] as const;

async function shot(page: Page, theme: string, width: number, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${name}-${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the on account payment at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const charcoalOn = async () => {
        if (theme === "charcoal") {
          await page.locator("hh-shell-header button.account").click();
          await page.getByRole("button", { name: "Theme: Light" }).click();
        }
      };
      // The band's account menu stays open after the theme tap, and on a phone it hangs over the
      // area switch. Escape closes it, the way it closes in every menu of the house.
      const closeAccountMenu = async () => {
        await page.keyboard.press("Escape");
      };

      await openDesk(page, request);
      await charcoalOn();
      await closeAccountMenu();

      // The on account state: INV-1001 alone can take $4,825.00, the cheque is $4,865.00, and the
      // $40.00 difference stays on the customer's account as a credit.
      await page.locator("[data-register-row]", { hasText: "INV-1001" }).getByRole("button", { name: "Record payment" }).click();
      await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
      await page.getByRole("button", { name: "Clear" }).first().click();
      await page.getByRole("checkbox", { name: "Pay INV-1001" }).click();
      const amount = page.locator("[data-field='receive-amount']");
      await amount.fill("4865.00");
      await amount.blur();
      await page.locator("[data-field='receive-reference']").fill("Cheque 4490");
      await expect(page.locator("[data-applied]")).toHaveText("$4,825.00 of $4,865.00");
      await expect(page.locator("[data-unapplied]")).toContainText("unapplied $40.00");
      await expect(page.locator("[data-on-account]")).toContainText("stays on the customer's account as a credit");
      // The hold strip sits under Record payment and Cancel, so its visibility is the shot's own
      // proof the page was read whole.
      await expect(page.locator("[data-hold-stays]")).toBeVisible();
      await shot(page, theme, size.width, "on-account");
    });
  }
}
