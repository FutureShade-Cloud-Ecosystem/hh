// Screenshots of the RT-3b screens (Due to release, the release and its confirm, the reversal, the lien doors and the
// old press's refusal), at 390 and 1280 px, light and charcoal, walked on the stub the way the Controller walks them.
// It runs only when LIEN_SHOT_DIR names a folder, and writes there and nowhere else:
//
//   LIEN_SHOT_DIR=/some/folder npx playwright test lien-due-shots
//
// Charcoal is the design system's dark half; one tap on the header's theme control moves between it and light.
import type { Page } from "@playwright/test";
import { join } from "node:path";
import { test, expect, openLiens, liensTab, dayFrom } from "./lien-helpers.js";

const dir = process.env.LIEN_SHOT_DIR ?? "";
test.skip(dir === "", "set LIEN_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `money-desk-due__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the due to release walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const due = (lien: string) => page.locator(`[data-row="due"][data-lien-job="${lien}"]:visible`);

      await openLiens(page, request, "controller");
      if (theme === "charcoal") {
        await page.locator("hh-shell-header button.account").click();
        await page.getByRole("button", { name: "Theme: Light" }).click();
      }
      await liensTab(page, "due");
      await expect(due("GLS-2100")).toBeVisible();
      await take("01", "list");
      await due("GLS-2100").getByRole("button", { name: "Release" }).click();
      await expect(page.locator("[data-due-panel]")).toBeVisible();
      await take("02", "release-asks-first");
      await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Released");
      await take("03", "released");
      await page.locator("[data-row='released']:visible").getByRole("button", { name: "Reverse" }).click();
      await expect(page.locator("[data-reverse-panel]")).toBeVisible();
      await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
      await expect(page.locator("[data-error]")).toContainText("Say why");
      await take("04", "reverse-needs-a-reason");
      await page.locator("[data-reverse-panel]").getByLabel("Reason").fill("Released against the wrong job");
      await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Reversed");
      await take("05", "reversed");

      await liensTab(page, "jobs");
      await page.locator("[data-row='job'][data-job-no='GLS-2100']:visible").getByRole("button", { name: "Open" }).click();
      await expect(page.locator("[data-doors]")).toBeVisible();
      await take("06", "doors");
      await page.locator("[data-doors]").getByLabel("Lien preserved on").fill(dayFrom(-2));
      await page.locator("[data-doors]").getByLabel("Note", { exact: true }).fill("Filed at the registry");
      await page.locator("[data-doors]").getByRole("button", { name: "Record preserved lien" }).click();
      await expect(page.locator("[data-lien-state]")).toContainText("has not been discharged");
      await take("07", "lien-recorded");
      await liensTab(page, "due");
      await expect(due("GLS-2100")).toContainText("Blocked by a preserved lien");
      await take("08", "blocked");

      // The General Manager is another person: a page of their own.
      const gm = await page.context().newPage();
      await gm.setViewportSize(size);
      await openLiens(gm, request, "general_manager");
      if (theme === "charcoal") {
        await gm.locator("hh-shell-header button.account").click();
        await gm.getByRole("button", { name: "Theme: Light" }).click();
      }
      await liensTab(gm, "holdbacks");
      await gm.locator("[data-row='aging'][data-job-no='GLS-2103']:visible").getByRole("button", { name: "Release" }).click();
      await expect(gm.locator("[data-job-level-refused]")).toBeVisible();
      await shot(gm, theme, size.width, "09", "old-press-refused");
    });
  }
}
