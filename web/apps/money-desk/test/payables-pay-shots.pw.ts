// The Payables board's Pay form photographed, in light and in charcoal, at 390, 1280 and 1440
// wide. The form is the Payables board's own: the same desk's fields, buttons and words, so it
// wears the desk's styles with nothing of its own.
//
// It runs only when MD_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test payables-pay-shots
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
  { width: 1440, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function shot(page: Page, theme: string, width: number, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${name}-${theme}-${width}.png`), fullPage: true });
}

/** The band's account menu, and the theme tap in it, for the dark half. */
async function charcoalOn(page: Page, theme: string): Promise<void> {
  if (theme !== "charcoal") return;
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
  // The band's account menu stays open after the theme tap, and on a phone it hangs over the area
  // switch. Escape closes it, the way it closes in every menu of the house.
  await page.keyboard.press("Escape");
}

/** The board, and a payee's open items, with the Pay control on the vendor bill. */
async function openPayeePage(page: Page): Promise<void> {
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.locator("[data-payables-aging]")).toBeVisible();
  await page.locator("[data-payee-row]", { hasText: "Saltmarsh Fasteners (demo)" }).getByRole("button").click();
  await expect(page.locator("[data-payables-lines]")).toBeVisible();
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the Pay form at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      // The Pay control is a manager's, so the form is photographed as the Financial Controller
      // sees it.
      await openDesk(page, request, "controller");
      await charcoalOn(page, theme);

      // The board, and a payee's open items: the vendor bill with the Pay control on it.
      await openPayeePage(page);
      await shot(page, theme, size.width, "pay-control");

      // The form itself, open on the part paid bill.
      await page.locator("[data-line-row]", { hasText: "B-4107" }).getByRole("button", { name: "Pay" }).click();
      await expect(page.locator("[data-pay-form]")).toBeVisible();
      await shot(page, theme, size.width, "pay-form");
    });
  }
}

// The unknown outcome: the answer to a payment that landed is lost on the way back, so the money
// may or may not have moved. The form says so, in the desk's own words.
for (const size of [SIZES[0]!, SIZES[1]!]) {
  for (const theme of THEMES) {
    test(`the unknown outcome at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDesk(page, request, "controller");
      await charcoalOn(page, theme);
      await openPayeePage(page);
      await page.locator("[data-line-row]", { hasText: "B-4107" }).getByRole("button", { name: "Pay" }).click();
      await expect(page.locator("[data-pay-form]")).toBeVisible();

      // The request is lost on the way: the payment did not land, but the desk cannot know that, so
      // the books still show the bill open and the form stays open with its error. (When the books
      // show the payment landed, the form closes by design.)
      await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
        await route.abort("connectionreset");
      });
      await page.locator("[data-field='pay-amount']").fill("100.00");
      await page.locator("[data-action='payables-pay-send']").click();
      await expect(page.locator("[data-pay-error]")).toContainText("may or may not have been recorded");
      // The books are read again behind the error; the shot waits for the form and the error to be
      // the last thing standing, so it shows both open.
      await expect(page.locator("[data-pay-form]")).toBeVisible();
      await page.waitForLoadState("networkidle");
      await expect(page.locator("[data-pay-error]")).toBeVisible();
      await page.locator("[data-pay-error]").scrollIntoViewIfNeeded();
      await shot(page, theme, size.width, "pay-unknown");
    });
  }
}

// A role that cannot pay (Payables and Receivables): the board and the bill with no Pay control.
for (const size of [SIZES[0]!, SIZES[1]!]) {
  for (const theme of THEMES) {
    test(`a role that cannot pay at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDesk(page, request);
      await charcoalOn(page, theme);
      await openPayeePage(page);
      await shot(page, theme, size.width, "pay-held-role");
    });
  }
}
