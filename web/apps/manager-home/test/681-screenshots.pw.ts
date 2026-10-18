// HH-681: screenshots of the Change orders tab and a decision card, light and brand.
import { test, expect, type Page } from "@playwright/test";
import { resetStub } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MH_SHOT_DIR ?? "";
test.skip(dir === "", "set MH_SHOT_DIR to take the screenshots");

test.use({ viewport: { width: 1440, height: 900 } });

async function shot(page: Page, theme: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `hh-681-change-orders__${name}__${theme}.png`), fullPage: false });
}

for (const theme of ["light", "brand"] as const) {
  test(`Change orders tab and card, ${theme}`, async ({ page, request }) => {
    test.setTimeout(60_000);
    await resetStub(request);
    await page.goto("/");
    await page.locator("[data-flow='general-manager']").click();
    await page.locator(".view-link", { hasText: "Approvals" }).click();
    await page.locator(".kind-tab", { hasText: "Change orders" }).click();
    await expect(page.locator("[data-approval]").first()).toBeVisible();
    if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
    await shot(page, theme, "tab");
    await page.locator("[data-approval]").nth(1).click();
    await expect(page.locator("[data-approval-detail]")).toContainText("CO-PLM-1002");
    await shot(page, theme, "card");
  });
}
