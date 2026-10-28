// UX-106 round 6: the pricing audit log, and a sweep of every table.
//
// The defect. The PRICING AUDIT log's table is six columns (When, Entity,
// Action, Field, Change, Actor), and at 390 it scrolled sideways: its
// scrollWidth was 578 in a 316 px card. It is the same defect the margin
// policy table had in round 1 and the audit list had in round 5, and it gets
// the same house answer: the card-row pattern.
//
// The sweep. The round's point is that no cut table is left for a later
// round. So the second leg walks EVERY view the round touches, at 390, in
// light and in charcoal, and measures EVERY table, EVERY table wrapper and
// every element that could spill its own box:
//
//   a table that scrolls sideways has scrollWidth greater than clientWidth;
//   a value that runs off the card is an element whose scrollWidth is
//   greater than its clientWidth while its own overflow is visible.
//
// Both are measured. Both are the numbers the defect is named in.
//
// The legs:
//
//   1. the pricing audit log fits a phone;
//   2. the sweep: no table on any view scrolls sideways at 390, in light or
//      in charcoal.
import { expect, test, type Locator, type Page } from "@playwright/test";

/** One measured box: what it is, and how far it scrolls sideways. */
interface Box {
  readonly view: string;
  readonly what: string;
  readonly scroll: number;
  readonly client: number;
}

/** The thirteen pricing views, the Section list's own words. */
const PRICING_VIEWS: readonly { readonly id: string; readonly title: string; readonly settle: string }[] = [
  { id: "margin", title: "Margin policy", settle: '[data-test="pr-margin-count"]' },
  { id: "rules", title: "Rules", settle: '[data-test="pr-rules-count"]' },
  { id: "customers", title: "Customer prices", settle: '[data-test="pr-cust-count"], [data-test="pr-cust-empty"]' },
  { id: "breaks", title: "Quantity breaks", settle: '[data-test="pr-breaks-count"]' },
  { id: "promos", title: "Promotions & clearance", settle: '[data-test="pr-promo-count"]' },
  { id: "rebates", title: "Rebates", settle: '[data-test="pr-rebate-count"]' },
  { id: "uom", title: "Units of measure", settle: '[data-test="pr-uom-count"]' },
  { id: "batches", title: "Batches & imports", settle: '[data-test="pr-batches-count"]' },
  { id: "pricefile", title: "Price file", settle: '[data-test="pr-pf-vendor-count"]' },
  { id: "lowmargin", title: "Low margin", settle: '[data-test="pr-lm-count"]' },
  { id: "audit", title: "Pricing audit", settle: '[data-test="pr-audit-count"]' },
  { id: "changelog", title: "Change log", settle: '[data-test="pr-log-count"]' },
  { id: "payment-terms", title: "Payment terms and deposits", settle: '[data-test="pricing-terms-empty"]' },
];

/** The demo yard account, the one customer the stub carries prices for. */
const DEMO_CUSTOMER = "cu110000-0000-4000-9000-0000000000a1";

const THEMES = ["light", "charcoal"] as const;

async function signIn(page: Page): Promise<void> {
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

/** The section opens through the app's own list; on a phone the list waits
 *  folded behind the Settings control (UX-104). */
async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The pricing view a walk stands on: the phone folds the whole list behind
 *  one select, so the select is what the walk moves. */
async function openPricingView(page: Page, title: string, settle: string): Promise<void> {
  await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/**
 * Every box on the screen that could cut: the table wrappers, the tables
 * that stand in one, and every element that spills its own box.
 *
 * The app renders into its own shadow root, so the walk reads there.
 */
async function measure(page: Page, view: string): Promise<Box[]> {
  return page.evaluate((name) => {
    const app = document.querySelector("dealer-admin-app");
    const root = app?.shadowRoot ?? document;
    const describe = (el: Element): string => {
      const cls = typeof el.className === "string" && el.className.trim() !== "" ? `.${el.className.trim().split(/\s+/).join(".")}` : "";
      const test = el.getAttribute("data-test");
      return `${el.tagName.toLowerCase()}${cls}${test === null ? "" : `[${test}]`}`;
    };
    const boxes: { view: string; what: string; scroll: number; client: number }[] = [];

    // The wrap holds the table, so the wrap is the box that scrolls.
    for (const wrap of Array.from(root.querySelectorAll(".tablewrap"))) {
      boxes.push({ view: name, what: describe(wrap), scroll: wrap.scrollWidth, client: wrap.clientWidth });
    }
    // A table that stands in no wrap is measured on its own.
    for (const table of Array.from(root.querySelectorAll("table"))) {
      if (table.closest(".tablewrap") !== null) continue;
      boxes.push({ view: name, what: describe(table), scroll: table.scrollWidth, client: table.clientWidth });
    }
    // And anything whose content runs past its own box: that is the cut
    // column, the value that left the card.
    for (const el of Array.from(root.querySelectorAll("*"))) {
      const cs = getComputedStyle(el);
      if (cs.display === "none" || cs.position === "absolute" || cs.position === "fixed") continue;
      if (cs.overflowX === "auto" || cs.overflowX === "scroll") continue;
      if (el.scrollWidth <= el.clientWidth + 1) continue;
      boxes.push({ view: name, what: describe(el), scroll: el.scrollWidth, client: el.clientWidth });
    }
    return boxes;
  }, view);
}

/** The page itself never scrolls sideways at a phone's width. */
async function pageScrolls(page: Page): Promise<{ scroll: number; client: number }> {
  return page.evaluate(() => ({ scroll: document.scrollingElement?.scrollWidth ?? 0, client: document.scrollingElement?.clientWidth ?? 0 }));
}

/** The pricing audit log's own table, the defect this round names. */
async function pricingAuditTable(page: Page): Promise<Locator> {
  await openSurface(page, "pricing", '[data-test="pr-margin-count"]');
  await openPricingView(page, "Pricing audit", '[data-test="pr-audit-count"]');
  return page.locator(".tablewrap").first();
}

// -- 1: the pricing audit log fits a phone -------------------------------------------------------------

test("the pricing audit log fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);

  const wrap = await pricingAuditTable(page);
  const state = await wrap.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(state.scroll, "the pricing audit log scrolls sideways under a cut column at 390").toBeLessThanOrEqual(state.client);

  // The card row: the table's head is gone and each row lays out as a grid.
  const table = page.locator("table").first();
  expect(await table.locator("thead").first().evaluate((el) => getComputedStyle(el).display), "the pricing audit log still shows a table head at 390").toBe("none");
  const row = table.locator("tbody tr").first();
  expect(await row.evaluate((el) => getComputedStyle(el).display), "the pricing audit log's row is not a card row at 390").toBe("grid");

  // Every cell of the card row names itself, so the pair reads without a head.
  const labels = await row.locator("td").evaluateAll((cells) => cells.map((cell) => cell.getAttribute("data-label") ?? ""));
  expect(labels, "the pricing audit log's card row does not label its pairs").toEqual(["When", "Entity", "Action", "Field", "Change", "Actor"]);
});

// -- 2: the sweep ---------------------------------------------------------------------------------------

for (const theme of THEMES) {
  test(`no table on any view scrolls sideways at 390, ${theme}`, async ({ page }) => {
    test.setTimeout(300_000);
    await page.setViewportSize({ width: 390, height: 844 });
    await signIn(page);
    if (theme === "charcoal") await goCharcoal(page);

    const boxes: Box[] = [];

    // The register.
    await openSurface(page, "staff", '[data-test="staff-count"]');
    boxes.push(...(await measure(page, "staff")));

    // Every pricing view, the Section list's own thirteen.
    await openSurface(page, "pricing", '[data-test="pr-margin-count"]');
    for (const v of PRICING_VIEWS) {
      await openPricingView(page, v.title, v.settle);
      if (v.id === "customers") {
        // The customer prices table only lands once a customer is asked for.
        await page.locator('[data-field="pr-cust-id"]').fill(DEMO_CUSTOMER);
        await page.locator('[data-action="pr-cust-load"]').click();
        await expect(page.locator('[data-test="pr-cust-count"]')).toBeVisible({ timeout: 30_000 });
        await page.waitForLoadState("networkidle");
      }
      boxes.push(...(await measure(page, `pricing:${v.id}`)));
    }

    // The audit trail: the list, one open drawer and one entity's whole trail.
    await openSurface(page, "audit", '[data-test="audit-count"]');
    boxes.push(...(await measure(page, "audit:list")));
    await page.locator("tr[data-action='open-audit-entry']").first().click();
    await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
    await page.waitForLoadState("networkidle");
    boxes.push(...(await measure(page, "audit:drawer")));
    await page.locator("[data-action='open-audit-trail']").first().click();
    await expect(page.locator("[data-test=audit-trail]").first()).toBeVisible({ timeout: 30_000 });
    await page.waitForLoadState("networkidle");
    boxes.push(...(await measure(page, "audit:trail")));

    // The roles grid.
    await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
    boxes.push(...(await measure(page, "roles:grid")));

    // The hotkey rosters.
    await openSurface(page, "hotkeys", '[data-test^="hotkeys-roster-"]');
    boxes.push(...(await measure(page, "hotkeys:rosters")));

    // The page itself never scrolls sideways.
    const scrolled = await pageScrolls(page);
    expect(scrolled.scroll, "the page scrolls sideways at 390").toBeLessThanOrEqual(scrolled.client);

    // Every box: the scrollWidth against the clientWidth.
    const cut = boxes.filter((b) => b.scroll > b.client + 1);
    const report = cut.map((b) => `  ${b.view} | ${b.what} | scroll ${b.scroll} client ${b.client}`).join("\n");
    expect(cut, `these boxes cut their content at 390 (${theme}):\n${report}`).toEqual([]);
  });
}
