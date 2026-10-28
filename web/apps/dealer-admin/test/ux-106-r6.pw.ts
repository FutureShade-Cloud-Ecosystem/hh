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

/** One measured box: what it is, how far it scrolls sideways, and whether it
 *  is a table, a value that ran off its box, or a value that reads in the
 *  label column. */
interface Box {
  readonly view: string;
  readonly what: string;
  readonly kind: "table" | "spill" | "stray";
  readonly scroll: number;
  readonly client: number;
}

/** The thirteen pricing views, the Section list's own words. Each settles on
 *  its LAST panel: a view reads its panels one after another (the promotions
 *  read lands before the clearance read, the rebate programs before the
 *  ledger), so settling on the first panel would measure a half-built view. */
const PRICING_VIEWS: readonly { readonly id: string; readonly title: string; readonly settle: string }[] = [
  { id: "margin", title: "Margin policy", settle: '[data-test="pr-margin-count"]' },
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

/** The pricing view a walk stands on: the phone folds the whole list behind
 *  one select, so the select is what the walk moves. */
async function openPricingView(page: Page, title: string, settle: string): Promise<void> {
  await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
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

/** Wait until the app stops changing. A view's panels read one after another,
 *  so a measurement taken the moment the settle marker appears can still miss
 *  a panel that lands a moment later. Two equal fingerprints in a row is the
 *  view at rest. */
async function waitForQuiet(page: Page): Promise<void> {
  let previous = await fingerprint(page);
  for (let beat = 0; beat < 50; beat += 1) {
    await page.waitForTimeout(120);
    const now = await fingerprint(page);
    if (now === previous) return;
    previous = now;
  }
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
      const label = el.getAttribute("data-label");
      return `${el.tagName.toLowerCase()}${cls}${test === null ? "" : `[${test}]`}${label === null ? "" : `{${label}}`}`;
    };
    /** The width of a label's widest word, drawn the way the cell draws it:
     *  the label's own font, its own letter spacing, its own capitals. A
     *  label wraps on its words, so the word is what has to fit the column. */
    const measureLabel = (text: string, cs: CSSStyleDeclaration): number => {
      const canvas = document.createElement("canvas");
      const ctx = canvas.getContext("2d");
      if (ctx === null) return 0;
      ctx.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      ctx.letterSpacing = cs.letterSpacing === "normal" ? "0px" : cs.letterSpacing;
      const drawn = cs.textTransform === "uppercase" ? text.toUpperCase() : text;
      return drawn
        .split(/\s+/)
        .filter((word) => word !== "")
        .reduce((widest, word) => Math.max(widest, ctx.measureText(word).width), 0);
    };
    const boxes: { view: string; what: string; kind: "table" | "spill" | "stray"; scroll: number; client: number }[] = [];

    // The wrap holds the table, so the wrap is the box that scrolls.
    for (const wrap of Array.from(root.querySelectorAll(".tablewrap"))) {
      boxes.push({ view: name, what: describe(wrap), kind: "table", scroll: wrap.scrollWidth, client: wrap.clientWidth });
    }
    // A table that stands in no wrap is measured on its own.
    for (const table of Array.from(root.querySelectorAll("table"))) {
      if (table.closest(".tablewrap") !== null) continue;
      boxes.push({ view: name, what: describe(table), kind: "table", scroll: table.scrollWidth, client: table.clientWidth });
    }
    // And anything whose content runs past its own box: that is the cut
    // column, the value that left the card. A form control is measured by its
    // own box instead: an input's value scrolling inside the field is the
    // field's own behaviour, not a cut column, and what matters is whether
    // the control itself fits where it sits.
    for (const el of Array.from(root.querySelectorAll("*"))) {
      const cs = getComputedStyle(el);
      if (cs.display === "none" || cs.position === "absolute" || cs.position === "fixed") continue;
      if (cs.overflowX === "auto" || cs.overflowX === "scroll") continue;
      const tag = el.tagName.toLowerCase();
      if (tag === "input" || tag === "select" || tag === "textarea") {
        const parent = el.parentElement;
        if (parent === null) continue;
        const own = el.getBoundingClientRect().width;
        const room = parent.getBoundingClientRect().width;
        if (own <= room + 1) continue;
        boxes.push({ view: name, what: describe(el), kind: "spill", scroll: Math.round(own), client: Math.round(room) });
        continue;
      }
      if (el.scrollWidth <= el.clientWidth + 1) continue;
      boxes.push({ view: name, what: describe(el), kind: "spill", scroll: el.scrollWidth, client: el.clientWidth });
    }
    // A card row's value reads in the value column. A cell that holds two
    // things (a name and its code, a scope and its detail) would otherwise
    // put the second of them in the label column, where the label reads.
    for (const cell of Array.from(root.querySelectorAll(".cardrow-table tbody td[data-label]"))) {
      const cs = getComputedStyle(cell);
      const tracks = cs.gridTemplateColumns.split(" ").map((track) => Number.parseFloat(track));
      if (tracks.length < 2 || Number.isNaN(tracks[0])) continue;
      const gap = Number.parseFloat(cs.columnGap) || 0;
      const valueLeft = cell.getBoundingClientRect().left + tracks[0] + gap;
      for (const part of Array.from(cell.children)) {
        const left = part.getBoundingClientRect().left;
        if (left >= valueLeft - 1) continue;
        boxes.push({
          view: name,
          what: `${describe(cell)} | ${describe(part)} reads in the label column`,
          kind: "stray",
          scroll: Math.round(left - cell.getBoundingClientRect().left),
          client: Math.round(tracks[0] + gap),
        });
      }
      // And the label fits its own column. The label is drawn by the cell's
      // own CSS, so it is measured the way it is drawn: the label's own font,
      // its own letter spacing, its own capitals.
      const label = cell.getAttribute("data-label") ?? "";
      const labelStyle = getComputedStyle(cell, "::before");
      const width = measureLabel(label, labelStyle);
      if (width <= tracks[0] + 0.5) continue;
      boxes.push({
        view: name,
        what: `${describe(cell)} | the label does not fit its column`,
        kind: "spill",
        scroll: Math.round(width),
        client: Math.round(tracks[0]),
      });
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
  await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
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
    const visited: string[] = [];

    // The register.
    await openSurface(page, "staff", '[data-test="staff-count"]');
    visited.push("staff");
    boxes.push(...(await measure(page, "staff")));

    // Every pricing view, the Section list's own thirteen.
    await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
    for (const v of PRICING_VIEWS) {
      await openPricingView(page, v.title, v.settle);
      if (v.id === "customers") {
        // The customer prices table only lands once a customer is asked for.
        await page.locator('[data-field="pr-cust-id"]').fill(DEMO_CUSTOMER);
        await page.locator('[data-action="pr-cust-load"]').click();
        await expect(page.locator('[data-test="pr-cust-count"]')).toBeVisible({ timeout: 30_000 });
        await page.waitForLoadState("networkidle");
        await waitForQuiet(page);
      }
      visited.push(`pricing:${v.id}`);
      boxes.push(...(await measure(page, `pricing:${v.id}`)));
    }

    // The audit trail: the list, one open drawer and one entity's whole trail.
    await openSurface(page, "audit", '[data-test="audit-count"]');
    visited.push("audit:list");
    boxes.push(...(await measure(page, "audit:list")));
    await page.locator("tr[data-action='open-audit-entry']").first().click();
    await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
    await page.waitForLoadState("networkidle");
    await waitForQuiet(page);
    visited.push("audit:drawer");
    boxes.push(...(await measure(page, "audit:drawer")));
    await page.locator("[data-action='open-audit-trail']").first().click();
    await expect(page.locator("[data-test=audit-trail]").first()).toBeVisible({ timeout: 30_000 });
    await page.waitForLoadState("networkidle");
    await waitForQuiet(page);
    visited.push("audit:trail");
    boxes.push(...(await measure(page, "audit:trail")));

    // The roles grid.
    await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
    visited.push("roles:grid");
    boxes.push(...(await measure(page, "roles:grid")));

    // The hotkey rosters.
    await openSurface(page, "hotkeys", '[data-test^="hotkeys-roster-"]');
    visited.push("hotkeys:rosters");
    boxes.push(...(await measure(page, "hotkeys:rosters")));

    // The editors. A table behind a click is a table still: the margin
    // policy's own floor panel, the break schedule's tiers, one batch's
    // lines, the price file's diff and the curation editor's own rows.
    await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
    await openPricingView(page, "Margin policy", '[data-test="pr-margin-count"]');
    await page.locator('[data-action="pr-policy-edit"]').first().click();
    await waitForQuiet(page);
    visited.push("pricing:margin-editor");
    boxes.push(...(await measure(page, "pricing:margin-editor")));

    await openPricingView(page, "Quantity breaks", '[data-test="pr-breaks-count"]');
    await page.locator('[data-action="pr-break-edit"]').first().click();
    await expect(page.locator('[data-field="pr-brk-item-0-from"]')).toBeVisible({ timeout: 30_000 });
    await waitForQuiet(page);
    visited.push("pricing:break-editor");
    boxes.push(...(await measure(page, "pricing:break-editor")));

    await openPricingView(page, "Batches & imports", '[data-test="pr-profiles-count"]');
    await page.locator('[data-action="pr-batch-open"]').first().click();
    await waitForQuiet(page);
    visited.push("pricing:batch-lines");
    boxes.push(...(await measure(page, "pricing:batch-lines")));

    await openPricingView(page, "Price file", '[data-test="pr-pf-runs-count"]');
    await page.locator('[data-field="pr-pf-vendor"]').selectOption({ label: "Northline Supply" });
    await page
      .locator('[data-field="pr-pf-file"]')
      .setInputFiles({ name: "price-file.csv", mimeType: "text/csv", buffer: Buffer.from("sku,retail,cost\nPAV-1,480,300\nPAV-2,510,320\nPAV-3,530,340\n") });
    await page.locator('[data-action="pr-pf-preview"]').click();
    await expect(page.locator('[data-test="pr-pf-preview"]')).toBeVisible({ timeout: 30_000 });
    await waitForQuiet(page);
    visited.push("pricing:pricefile-preview");
    boxes.push(...(await measure(page, "pricing:pricefile-preview")));

    await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
    await page.locator('[data-action="open-config-editor"]').first().click();
    await waitForQuiet(page);
    visited.push("roles:editor");
    boxes.push(...(await measure(page, "roles:editor")));

    // The sweep walked every view it names: a view that stopped rendering must
    // fail the leg, not quietly shrink the measurement. And the views that
    // carry a table must still carry one.
    expect(visited.length, "the sweep did not walk every view").toBe(24);
    const withTable = new Set(boxes.filter((b) => b.kind === "table").map((b) => b.view));
    expect(withTable.size, "the sweep measured no table on some views").toBeGreaterThanOrEqual(21);

    // The page itself never scrolls sideways.
    const scrolled = await pageScrolls(page);
    expect(scrolled.scroll, "the page scrolls sideways at 390").toBeLessThanOrEqual(scrolled.client);

    // Every box: the scrollWidth against the clientWidth. A value that reads
    // in the label column is the other way round: it sits before the column
    // it belongs in.
    const cut = boxes.filter((b) => (b.kind === "stray" ? b.scroll < b.client : b.scroll > b.client + 1));
    const report = cut.map((b) => `  ${b.view} | ${b.what} | scroll ${b.scroll} client ${b.client}`).join("\n");
    expect(cut, `these boxes cut their content at 390 (${theme}):\n${report}`).toEqual([]);
  });
}
