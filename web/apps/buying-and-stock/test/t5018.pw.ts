// The vendor's T5018 block walked in a real browser against the stub, with the visual read's rules
// held as legs: the block's own save sits inside it and says it saves only the block, the marked
// state, the number refusal and the saved notice read in a bookkeeper's words, and a seat that
// may not set the mark sees one plain line. The seat wears its real role label.
//
// With BS_SHOT_DIR set the legs also photograph each state, light and charcoal, at 1280 and 390:
//
//   BS_SHOT_DIR=/some/folder npx playwright test t5018
import type { Page } from "@playwright/test";
import { join } from "node:path";
import { test, expect, resetStub, stubRole, roleHandoff, openAreaAs, AP } from "./helpers.js";

const dir = process.env.BS_SHOT_DIR ?? "";
const SIZES = [{ width: 1280, height: 800 }, { width: 390, height: 844 }];
const CONTROLLER = { roleCode: "financial_controller", userId: "de110000-0000-4000-9000-000000000008", roleLabel: "Financial Controller" };

async function charcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
  await page.keyboard.press("Escape");
}

async function shoot(page: Page, name: string, theme: string, width: number): Promise<void> {
  if (dir === "") return;
  await page.screenshot({ path: join(dir, `${name}-${theme}-${width}.png`), fullPage: true });
}

async function openVendor(page: Page): Promise<void> {
  await page.locator("[data-vendor-row]").first().locator("button").first().click();
  await expect(page.locator("[data-vendor-panel]")).toBeVisible();
}

for (const size of SIZES) {
  for (const theme of ["light", "charcoal"] as const) {
    test(`controller marks a vendor ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await resetStub(request);
      await stubRole(request, "financial-controller");
      await page.addInitScript(roleHandoff, CONTROLLER);
      await page.goto("/");
      await page.locator("nav.areas").getByRole("button", { name: "Vendors" }).click();
      if (theme === "charcoal") await charcoal(page);
      await openVendor(page);
      const block = page.locator("[data-t5018]");
      await expect(block.locator("[data-t5018-save-hint]")).toHaveText("Saves only this block");
      await expect(block.getByRole("button", { name: "Save the T5018 record" })).toBeVisible();
      await expect(page.locator("hh-shell-header")).not.toContainText("financial_controller");
      await shoot(page, "mark-controller", theme, size.width);

      await block.getByRole("checkbox").check();
      await block.locator("[data-v-bn]").fill("12345");
      await block.getByRole("button", { name: "Save the T5018 record" }).click();
      await expect(page.locator("[data-error]")).toContainText("nine digits");
      await expect(block.locator("[data-v-bn]")).toHaveAttribute("aria-invalid", "true");
      const errorMarginTop = await page.locator("[data-error]").evaluate((el) => parseFloat(getComputedStyle(el).marginTop));
      expect(errorMarginTop).toBeGreaterThanOrEqual(8);
      await shoot(page, "mark-error", theme, size.width);

      await block.locator("[data-v-bn]").fill("123456789");
      await block.getByRole("button", { name: "Save the T5018 record" }).click();
      await expect(page.locator("[data-notice]")).toContainText("is marked as a subcontractor");
      await expect(block.locator("[data-t5018-save-hint]")).toBeVisible();
      await shoot(page, "mark-saved", theme, size.width);
    });

    test(`another seat cannot mark ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openAreaAs(page, request, AP, "Vendors");
      if (theme === "charcoal") await charcoal(page);
      await openVendor(page);
      await expect(page.locator("hh-shell-header button.account")).toContainText("Payables and Receivables");
      await expect(page.locator("hh-shell-header button.account")).not.toContainText("payables_receivables");
      await expect(page.locator("[data-t5018-note]")).toContainText("Financial Controller's to set");
      await expect(page.locator("[data-t5018]")).toHaveCount(0);
      await shoot(page, "mark-other", theme, size.width);
    });

    test(`bill entry offers goods or services on a line with no PO ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openAreaAs(page, request, AP, "Vendor bills");
      if (theme === "charcoal") await charcoal(page);
      await expect(page.locator("hh-shell-header button.account")).toContainText("Payables and Receivables");
      await page.getByRole("button", { name: "Enter a bill" }).click();
      await page.locator("[data-bill-po]").selectOption({ label: "PO-PLM-67 · Unilock (demo)" });
      if (size.width >= 1280) {
        const poBox = await page.locator("[data-bill-po]").boundingBox();
        expect(poBox?.width ?? 0).toBeGreaterThanOrEqual(350);
      }
      await page.locator("[data-bill-no-field]").fill("INV-9052");
      await page.locator("[data-extra-desc]").fill("Subcontractor grading");
      await page.locator("[data-extra-qty]").fill("1");
      await page.locator("[data-extra-cost]").fill("480.00");
      await expect(page.locator("[data-bill-entry]")).toContainText(
        "Not marked is the default and counts as services on T5018 until marked.",
      );
      const group = page.getByRole("group", { name: "Goods or services" });
      await expect(group.getByRole("button", { name: "Not marked" })).toHaveAttribute("aria-pressed", "true");
      await group.getByRole("button", { name: "Services" }).click();
      await expect(group.getByRole("button", { name: "Services" })).toHaveAttribute("aria-pressed", "true");
      await shoot(page, "bill-entry-no-po", theme, size.width);
      await page.getByRole("button", { name: "Enter the bill" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Entered bill INV-9052 as a draft.");
    });
  }
}
