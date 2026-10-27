// Screenshots of the invoice panel's credits section (FW1-12b: open credit memos and unapplied
// account credits applied to later invoices, and a reversal), at 390 and 1440 px, light and brand
// (the design system's dark half, the charcoal shots), walked on the stub the way the role walks
// them. The visual reference is the receivables office's own Reverse form. It runs only when
// MD_SHOT_DIR names a folder (so the suite's own run skips it), and writes there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test office-credits-shots
import { join } from "node:path";
import { test, expect, openDesk } from "./helpers.js";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the invoice's credits at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(90_000);
      await page.setViewportSize(size);
      const take = async (state: string) => {
        await page.waitForLoadState("networkidle");
        await page.screenshot({ path: join(dir, `office-invoice-credits__${state}-${theme}-${size.width}.png`), fullPage: true });
      };
      const charcoalOn = async () => {
        if (theme === "brand") {
          await page.locator("hh-shell-header button.account").click();
          await page.getByRole("button", { name: "Theme: Light" }).click();
        }
      };

      await openDesk(page, request);
      await charcoalOn();
      await page.locator("[data-register-row]", { hasText: "Granite Edge Landscaping" }).first().getByRole("button").click();
      await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
      await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "INV-1002" }).click();
      await expect(page.locator("[data-open-credit-row]", { hasText: "CM-0001" })).toBeVisible();
      await take("credits");

      await page.locator("[data-open-credit-row]", { hasText: "CM-0001" }).getByRole("button", { name: "Apply credit" }).click();
      await expect(page.locator("[data-credit-apply-form]")).toBeVisible();
      await take("apply-form");

      await page.locator("[data-field='credit-apply-amount']").fill("100.00");
      await page.locator("[data-field='credit-apply-reason']").fill("Applying open credit memo to balance");
      await page.getByRole("button", { name: "Apply to invoice" }).click();
      await expect(page.locator("[data-credit-app-row]", { hasText: "CM-0001" })).toContainText("Applied");
      await page.locator("[data-credit-app-row]", { hasText: "CM-0001" }).getByRole("button", { name: "Reverse credit" }).click();
      await expect(page.locator("[data-credit-reverse-form]")).toBeVisible();
      await take("reverse-form");
    });
  }
}
