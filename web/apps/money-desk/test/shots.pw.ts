// Screenshots of every receivables screen, at 390 and 1440 px, light and brand,
// walked on the stub the way the role walks them. Each file is named beside its
// reference in hh-sup/design-refs/money-desk (<screen>__<state>-<theme>-<size>).
// It runs only when MD_SHOT_DIR names a folder (so the suite's own run skips it),
// and writes there and nowhere else:
//
//   MD_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's default,
// one tap on the header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openDesk, receivablesHandoff, resetStub } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, screen: string, state: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${screen}__${state}-${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request, context }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (screen: string, state: string, on: Page = page) => shot(on, theme, size.width, screen, state);
      const brandOn = async (on: Page = page) => {
        if (theme === "brand") await on.getByRole("button", { name: "Theme: Light" }).click();
      };

      await openDesk(page, request);
      await brandOn();
      await take("receivables-home", "home");

      await page.locator("[data-bounced-row]").getByRole("button").click();
      await expect(page.locator("[data-bounced-work]")).toBeVisible();
      await take("receivables-home", "bounced");

      await page.locator("[data-register-row]", { hasText: "INV-1001" }).getByRole("button", { name: "Record payment" }).click();
      await expect(page.locator("[data-field='receive-amount']")).not.toHaveValue("");
      await page.locator("[data-field='receive-amount']").fill("5000.00");
      await page.getByRole("button", { name: "Oldest first" }).click();
      await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
      await expect(page.locator("[data-applied]")).toHaveText("$5,000.00 of $5,000.00");
      await take("account-open-invoices", "apply-payment");

      await page.getByRole("button", { name: "Statement", exact: true }).click();
      await page.getByRole("button", { name: "Run a statement" }).click();
      await expect(page.locator("[data-closing]")).toBeVisible();
      await take("account-open-invoices", "statement");

      await page.getByRole("button", { name: "‹ Receivables" }).click();
      await page.getByRole("button", { name: "Credit holds", exact: true }).click();
      await page.getByRole("button", { name: /Place a hold/ }).click();
      await expect(page.locator("[data-hold-panel]")).toContainText("later");
      await take("credit-hold", "place");

      await resetStub(request);
      const boss = await context.newPage();
      await boss.setViewportSize(size);
      await boss.addInitScript(receivablesHandoff as (who: string) => void, "controller");
      await boss.goto("/");
      await expect(boss.locator("[data-card]")).toHaveCount(4);
      await brandOn(boss);
      await boss.getByRole("button", { name: "Credit holds", exact: true }).click();
      await boss.locator("[data-hold-row]", { hasText: "Granite" }).getByRole("button", { name: "Lift..." }).click();
      await boss.locator("[data-field='release-reason']").fill("Payment applied; promise open on the balance");
      await take("credit-hold", "lift", boss);
    });
  }
}
