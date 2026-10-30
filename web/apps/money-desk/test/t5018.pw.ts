// The T5018 year report walked in a real browser against the stub, with the visual read's rules
// held as legs: the business number is labelled and its missing mark is the desk's warning pill,
// the two empties each say their own claim, and the file is not offered on an empty year.
//
// With MD_SHOT_DIR set the same legs also photograph each state, light and charcoal, at 1280 and
// 390. The folder is where the shots land and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test t5018
import type { Page } from "@playwright/test";
import { join } from "node:path";
import { test, expect, openDesk } from "./helpers.js";

const dir = process.env.MD_SHOT_DIR ?? "";
const SIZES = [{ width: 1280, height: 800 }, { width: 390, height: 844 }];

/** Answers the report route with this body, whatever the year. */
async function answerWith(page: Page, body: unknown): Promise<void> {
  await page.route(
    (url) => /\/api\/v1\/purchasing\/t5018(\?|$)/.test(url.pathname + url.search),
    (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }),
  );
}

/** Opens the Payables area on its T5018 view. */
async function openReport(page: Page): Promise<void> {
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await page.getByRole("button", { name: "T5018", exact: true }).click();
  await expect(page.locator("[data-t5018-table], [data-t5018-empty]").first()).toBeVisible();
}

/** Moves the desk to charcoal through the account menu, whose word names the theme it is in. */
async function charcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
  await page.keyboard.press("Escape");
}

async function shoot(page: Page, name: string, theme: string, width: number): Promise<void> {
  if (dir === "") return;
  await page.screenshot({ path: join(dir, `${name}-${theme}-${width}.png`), fullPage: true });
}

const ROW = {
  vendor_id: "11111111-1111-4111-8111-111111111111",
  vendor_no: "VM-0001",
  name: "Marked (demo)",
  business_number: "123456789",
  business_number_missing: false,
  paid_cents: 125000,
};

for (const size of SIZES) {
  for (const theme of ["light", "charcoal"] as const) {
    test(`filled report ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDesk(page, request);
      if (theme === "charcoal") await charcoal(page);
      await openReport(page);
      const missing = page.locator("[data-t5018-missing]");
      await expect(missing.first()).toBeVisible();
      await expect(missing.first()).toHaveText("(missing)");
      await expect(missing.first()).toHaveClass(/pill-watch/);
      await expect(page.locator("[data-t5018-number]").first()).toHaveAttribute("data-label", "CRA business number");
      if (size.width < 600) {
        const label = await page.locator("[data-t5018-number]").first().evaluate((cell) => getComputedStyle(cell, "::before").content);
        expect(label).toContain("CRA business number");
      } else {
        await expect(page.locator("[data-t5018-table] thead")).toContainText("CRA business number");
      }
      await expect(page.getByRole("button", { name: "Download CSV" })).toBeEnabled();
      await shoot(page, "report-filled", theme, size.width);
    });

    test(`no vendor marked ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await answerWith(page, { year: 2026, items: [], total_cents: 0 });
      await openDesk(page, request);
      if (theme === "charcoal") await charcoal(page);
      await openReport(page);
      const empty = page.locator("[data-t5018-empty]");
      await expect(empty).toHaveAttribute("data-t5018-empty", "none-marked");
      await expect(empty).toContainText("No vendor is marked yet");
      await expect(empty).not.toContainText("Nothing was paid");
      await expect(page.getByRole("button", { name: "Download CSV" })).toBeDisabled();
      await shoot(page, "report-empty", theme, size.width);
    });

    test(`nothing paid ${theme} ${size.width}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await answerWith(page, { year: 2026, items: [{ ...ROW, paid_cents: 0 }], total_cents: 0 });
      await openDesk(page, request);
      if (theme === "charcoal") await charcoal(page);
      await openReport(page);
      const empty = page.locator("[data-t5018-empty]");
      await expect(empty).toHaveAttribute("data-t5018-empty", "none-paid");
      await expect(empty).toContainText("Nothing was paid to marked vendors in 2026");
      await expect(empty).not.toContainText("No vendor is marked");
      await expect(page.getByRole("button", { name: "Download CSV" })).toBeDisabled();
      await shoot(page, "report-unpaid", theme, size.width);
    });
  }
}
