// The credit change screens (CC-2) photographed in light and in charcoal, at 1280 and 390 wide,
// beside the Receivables desk and the Payables board for the side by side review.
//
// It runs only when MD_SHOT_DIR names a folder (so the suite's own run skips it):
//
//   MD_SHOT_DIR=/some/folder npx playwright test credit-changes-shots
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
    const charcoalOn = async (page: Page) => {
      if (theme === "charcoal") {
        await page.locator("hh-shell-header button.account").click();
        await page.getByRole("button", { name: "Theme: Light" }).click();
        await page.keyboard.press("Escape");
      }
    };

    test(`the requester's credit change screens at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);

      // The requester: Payables and Receivables asks, reads the history, cannot decide.
      await openDesk(page, request);
      await charcoalOn(page);
      await shot(page, theme, size.width, "reference-receivables");
      await page.locator("[data-register-row]", { hasText: "Granite Edge Landscaping" }).first().getByRole("button").click();
      await page.locator("button[data-action='sub'][data-choice='credit']").click();
      await expect(page.locator("[data-credit-limit]")).toBeVisible();
      await shot(page, theme, size.width, "account-credit-limit");
      await page.locator("[data-field='credit-limit-amount']").fill("90000.00");
      await page.locator("[data-field='credit-limit-reason']").fill("Two large jobs");
      await page.getByRole("button", { name: "Request the change" }).click();
      await expect(page.locator("[data-credit-pending]").first()).toBeVisible();
      await shot(page, theme, size.width, "account-request-waiting");
      await page.getByRole("button", { name: "‹ Receivables" }).first().click();
      await page.getByRole("button", { name: "Credit changes" }).click();
      await expect(page.locator("[data-creditchanges]")).toBeVisible();
      await shot(page, theme, size.width, "waiting-no-controls");

    });

    test(`the approver's credit change screens at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      // The approver: the Financial Controller decides.
      await openDesk(page, request, "controller");
      await charcoalOn(page);
      await page.getByRole("button", { name: "Credit changes" }).click();
      await expect(page.locator("[data-creditchanges] [data-creditchange-row]")).toHaveCount(2);
      await shot(page, theme, size.width, "waiting-approver");
      await page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "First of two" }).getByRole("button", { name: "Approve" }).click();
      await expect(page.locator("[data-error]")).toBeVisible();
      await shot(page, theme, size.width, "refusal-in-server-words");
      await page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "The winter work" }).getByRole("button", { name: /^Reject/ }).click();
      await expect(page.locator("[data-field='credit-reject-reason']")).toBeVisible();
      await shot(page, theme, size.width, "reject-form");
    });
  }
}
