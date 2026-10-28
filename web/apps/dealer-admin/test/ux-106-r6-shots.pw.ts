// UX-106 round 6's shots: every table the round fixed, at a phone's width and
// at the desk's, light and charcoal. It runs only when DA_SHOT_DIR names a
// folder (so the suite's own run skips it), and writes there and nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-106-r6-shots
//
// The names are the item's: <section>__<state>__<theme>-<width>.png. The
// charcoal half is the design system's brand (dark) theme, one press of the
// band's theme control away from the light default.
//
// The desk width is 1440, the audit's own desk width (round 5 shot the trail
// at 1440; the pricing views and the roles grid are one surface now and they
// share a width).
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

/** The thirteen pricing views, the Section list's own words. */
const PRICING_VIEWS: readonly { readonly id: string; readonly title: string; readonly settle: string }[] = [
  { id: "rules", title: "Rules", settle: '[data-test="pr-rules-count"]' },
  { id: "customers", title: "Customer prices", settle: '[data-test="pr-cust-empty"]' },
  { id: "breaks", title: "Quantity breaks", settle: '[data-test="pr-breaks-count"]' },
  { id: "promos", title: "Promotions & clearance", settle: '[data-test="pr-clear-count"]' },
  { id: "rebates", title: "Rebates", settle: '[data-test="pr-ledger-total"]' },
  { id: "uom", title: "Units of measure", settle: '[data-test="pr-uom-count"]' },
  { id: "batches", title: "Batches & imports", settle: '[data-test="pr-profiles-count"]' },
  { id: "pricefile", title: "Price file", settle: '[data-test="pr-pf-runs-count"]' },
  { id: "lowmargin", title: "Low margin", settle: '[data-test="pr-lm-count"]' },
  { id: "audit", title: "Pricing audit", settle: '[data-test="pr-audit-count"]' },
  { id: "changelog", title: "Change log", settle: '[data-test="pr-log-count"]' },
];

/** The demo yard account, the one customer the stub carries prices for. */
const DEMO_CUSTOMER = "cu110000-0000-4000-9000-0000000000a1";

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
/** The settle marker: at least one of the selectors is visible. The pricing
 *  surface needs it, because the phone folds its list behind a select and
 *  the desk keeps a strip of pills, and only one of the two is on screen. */
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
    test(`every fixed table at ${size.width} px, ${theme}`, async ({ page }) => {
      test.setTimeout(300_000);
      await page.setViewportSize(size);
      await openDealerAdmin(page);
      if (theme === "charcoal") await goCharcoal(page);

      // The pricing views: each one holds a table the round fixed.
      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      for (const v of PRICING_VIEWS) {
        await openPricingView(page, v.id, v.title, v.settle);
        if (v.id === "customers") {
          // The customer prices table only lands once a customer is asked for.
          await page.locator('[data-field="pr-cust-id"]').fill(DEMO_CUSTOMER);
          await page.locator('[data-action="pr-cust-load"]').click();
          await expect(page.locator('[data-test="pr-cust-count"]')).toBeVisible({ timeout: 30_000 });
          await page.waitForLoadState("networkidle");
          await waitForQuiet(page);
        }
        await take(page, "pricing", v.id, theme, size.width);
      }

      // The break editor's own tiers: a table the round fixed.
      await openPricingView(page, "breaks", "Quantity breaks", '[data-test="pr-breaks-count"]');
      await page.locator('[data-action="pr-break-edit"]').first().click();
      await expect(page.locator('[data-field="pr-brk-item-0-from"]')).toBeVisible({ timeout: 30_000 });
      await waitForQuiet(page);
      await take(page, "pricing", "break-editor", theme, size.width);

      // The margin policy editor's own floor panel.
      await openPricingView(page, "margin", "Margin policy", '[data-test="pr-margin-count"]');
      await page.locator('[data-action="pr-policy-edit"]').first().click();
      await waitForQuiet(page);
      await take(page, "pricing", "margin-editor", theme, size.width);

      // One batch's lines: a table the round fixed.
      await openPricingView(page, "batches", "Batches & imports", '[data-test="pr-profiles-count"]');
      await page.locator('[data-action="pr-batch-open"]').first().click();
      await waitForQuiet(page);
      await take(page, "pricing", "batch-lines", theme, size.width);

      // The price file's own diff: the table the round fixed.
      await openPricingView(page, "pricefile", "Price file", '[data-test="pr-pf-runs-count"]');
      await page.locator('[data-field="pr-pf-vendor"]').selectOption({ label: "Northline Supply" });
      await page
        .locator('[data-field="pr-pf-file"]')
        .setInputFiles({ name: "price-file.csv", mimeType: "text/csv", buffer: Buffer.from("sku,retail,cost\nPAV-1,480,300\nPAV-2,510,320\nPAV-3,530,340\n") });
      await page.locator('[data-action="pr-pf-preview"]').click();
      await expect(page.locator('[data-test="pr-pf-preview"]')).toBeVisible({ timeout: 30_000 });
      await waitForQuiet(page);
      await take(page, "pricing", "pricefile-preview", theme, size.width);

      // The roles grid: the matrix and the curation grid.
      await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
      await take(page, "roles", "grid", theme, size.width);

      // The curation editor: its own draft rows and my rows.
      await page.locator('[data-action="open-config-editor"]').first().click();
      await waitForQuiet(page);
      await take(page, "roles", "editor", theme, size.width);

      // The audit drawer: its own pairs.
      await openSurface(page, "audit", '[data-test="audit-count"]');
      await page.locator("tr[data-action='open-audit-entry']").first().click();
      await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await waitForQuiet(page);
      await take(page, "audit", "drawer", theme, size.width);
    });
  }
}
