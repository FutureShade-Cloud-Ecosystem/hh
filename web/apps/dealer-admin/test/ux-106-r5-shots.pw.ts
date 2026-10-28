// UX-106 round 5's shots: the audit trail's list, one open entry's drawer and
// one entity's whole trail, at a phone's width and at the desk's, light and
// charcoal. It runs only when DA_SHOT_DIR names a folder (so the suite's own
// run skips it), and writes there and nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-106-r5-shots
//
// The names are the item's: <section>__<state>__<theme>-<width>.png. The
// charcoal half is the design system's brand (dark) theme, one press of the
// band's theme control away from the light default.
//
// The desk width is 1440, the audit's own desk width (round 4 shot the trail
// at 1280; the list and the trail are one surface now and they share a
// width).
//
// Each shot waits for its own content, never for a control: the drawer's own
// state attribute says the entry landed, and the trail's own card says its
// read landed.
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "charcoal"] as const;

async function openDealerAdmin(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The charcoal half is the design system's brand theme, one press away. */
async function goCharcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header .account").click();
  await page.locator('[data-testid="theme-control"]').click();
  await page.locator("hh-shell-header .account").click();
}

/** The audit surface opens through the app's own list; on a phone the list
 *  waits folded behind the Settings control (UX-104). */
async function openAudit(page: Page): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator("[data-test=audit-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function take(page: Page, section: string, state: string, theme: string, width: number): Promise<void> {
  await page.screenshot({
    path: join(dir, `${section}__${state}__${theme}-${width}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the audit trail at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") await goCharcoal(page);
      await openAudit(page);
      await take(page, "audit", "list", theme, size.width);

      // One open entry: the drawer holds the branch, the entity and the ids.
      await page.locator("tr[data-action='open-audit-entry']").first().click();
      await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await take(page, "audit", "drawer", theme, size.width);

      // One entity's whole trail: the card's own header carries the id.
      await page.locator("[data-action='open-audit-trail']").first().click();
      await expect(page.locator("[data-test=audit-trail]").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await take(page, "audit", "trail", theme, size.width);
    });
  }
}
