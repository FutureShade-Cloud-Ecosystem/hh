// UX-106 round 7's shots: the low margin view, at a phone's width and at the
// desk's, light and charcoal.
//
// It runs only when DA_SHOT_DIR names a folder (so the suite's own run skips
// it), and writes there and nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-106-r7-shots
//
// The names are the item's: <section>__<state>__<theme>-<width>.png. The
// charcoal half is the design system's brand (dark) theme, one press of the
// band's theme control away from the light default.
//
// The desk width is 1440, the width round 6 shot the pricing views at.
//
// UX-106 round 7: the low margin record's Level cell held the level chip and
// the clearance chip together, and the clearance chip broke onto a second
// line. The clearance attribution is its own pair now, so the record is shot
// again, in both themes, at both widths.
//
// Each shot waits for its own content, never for a control: the view's own
// count says its read landed, and the walk waits for the app to stop changing
// before it shoots, so a panel that lands a moment later is in the shot.
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

/** The surface opens through the app's own list; on a phone the list waits
 *  folded behind the Settings control (UX-104). */
async function expectVisible(page: Page, settle: string): Promise<void> {
  const visible = page.locator(settle.split(",").map((s) => `${s.trim()}:visible`).join(", ")).first();
  await expect(visible).toBeVisible({ timeout: 30_000 });
}

async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    const list = page.locator(".surface-list");
    if (!(await list.isVisible())) {
      await page.locator('[data-action="toggle-surface-list"]').click();
    }
    await expect(list).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expectVisible(page, settle);
  await page.waitForLoadState("networkidle");
  await waitForQuiet(page);
}

/** The pricing view: the phone folds the whole list behind one select, and
 *  the desk keeps the strip of pills. */
async function openPricingView(page: Page, id: string, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab[data-tab="${id}"]`).click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  await waitForQuiet(page);
}

/** A fingerprint of the app's own shadow root. */
async function fingerprint(page: Page): Promise<string> {
  return page.evaluate(() => {
    const app = document.querySelector("dealer-admin-app");
    const root = app?.shadowRoot;
    if (root === undefined || root === null) return "no-root";
    return `${root.innerHTML.length}:${root.querySelectorAll("table").length}`;
  });
}

/** Wait until the app stops changing: a panel that reads a moment later must
 *  be in the shot. */
async function waitForQuiet(page: Page): Promise<void> {
  let previous = await fingerprint(page);
  for (let beat = 0; beat < 50; beat += 1) {
    await page.waitForTimeout(120);
    const now = await fingerprint(page);
    if (now === previous) return;
    previous = now;
  }
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
    test(`the low margin view at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(300_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") await goCharcoal(page);

      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      await openPricingView(page, "lowmargin", "Low margin", '[data-test="pr-lm-count"]');
      await take(page, "pricing", "lowmargin", theme, size.width);
    });
  }
}
