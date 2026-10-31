// UX-106 round 7: a chip stays on one line, and the sweep names each state.
//
// K3's read of rounds 5 and 6 (reviews/REVIEW-PR658-r6-kimi.md) left two
// things for this round.
//
// P2, a chip that wraps. In the low margin view at 390 the second record,
// SO-2077, carries the level chip "critical" and, in the same value cell, the
// clearance chip "cleared: Branch manager (demo)". The clearance chip breaks
// before "(demo)" and takes two lines instead of one. The level and the
// clearance attribution shared one value cell (views-pricing.ts:2334-2337)
// and the shared rule let a chip's text wrap (styles.ts:393-399). A chip is
// one line: it is a pill, not a paragraph.
//
// P3, a sweep that guards totals and not identities. The walk covered the
// twenty-four states it names, but its guards were a count of states and a
// floor on the states that carry a table. Twenty-four pushes can hold a
// duplicate and still pass, and a lost table in one view can hide behind a
// table gained in another. The walk now runs off ONE manifest: each state
// says how to open it and how many tables it must carry (zero included), and
// the walked set must equal the manifest's set exactly.
//
// The legs:
//
//   1. no table on any view scrolls sideways at 390, in light and in charcoal;
//      every state carries its own tables; every chip in a card row is one
//      line.
//
// The walk itself is round 6's, strengthened: it moved here because the
// manifest is the walk's source now, and the suite walks the views once.
import { expect, test, type Page } from "@playwright/test";

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

/** A chip in a card row, and the number of lines it takes there. */
interface Chip {
  readonly view: string;
  readonly what: string;
  readonly lines: number;
}

/** One state of the app the walk visits: how to open it, and the number of
 *  tables it must carry. Zero is a value too: the payment terms and the
 *  hotkey rosters carry no table, and saying so keeps a table that appears
 *  there from passing unnoticed. */
interface State {
  readonly id: string;
  readonly tables: number;
  readonly open: (page: Page) => Promise<void>;
}

/** The thirteen pricing views, the Section list's own words. Each settles on
 *  its LAST panel: a view reads its panels one after another (the promotions
 *  read lands before the clearance read, the rebate programs before the
 *  ledger), so settling on the first panel would measure a half-built view.
 *  Each view says how many tables it carries. */
const PRICING_VIEWS: readonly { readonly id: string; readonly title: string; readonly settle: string; readonly tables: number }[] = [
  { id: "margin", title: "Margin policy", settle: '[data-test="pr-margin-count"]', tables: 1 },
  { id: "rules", title: "Rules", settle: '[data-test="pr-rules-count"]', tables: 1 },
  { id: "customers", title: "Customer prices", settle: '[data-test="pr-cust-empty"]', tables: 1 },
  { id: "breaks", title: "Quantity breaks", settle: '[data-test="pr-breaks-count"]', tables: 1 },
  { id: "promos", title: "Promotions & clearance", settle: '[data-test="pr-clear-count"]', tables: 2 },
  { id: "rebates", title: "Rebates", settle: '[data-test="pr-ledger-total"]', tables: 2 },
  { id: "uom", title: "Units of measure", settle: '[data-test="pr-uom-count"]', tables: 1 },
  { id: "batches", title: "Batches & imports", settle: '[data-test="pr-profiles-count"]', tables: 2 },
  { id: "pricefile", title: "Price file", settle: '[data-test="pr-pf-runs-count"]', tables: 2 },
  { id: "lowmargin", title: "Low margin", settle: '[data-test="pr-lm-count"]', tables: 1 },
  { id: "audit", title: "Pricing audit", settle: '[data-test="pr-audit-count"]', tables: 1 },
  { id: "changelog", title: "Change log", settle: '[data-test="pr-log-count"]', tables: 1 },
  { id: "payment-terms", title: "Payment terms and deposits", settle: '[data-test="pricing-terms-empty"]', tables: 0 },
];

/** The demo yard account, the one customer the stub carries prices for. */
const DEMO_CUSTOMER = "cu110000-0000-4000-9000-0000000000a1";

const THEMES = ["light", "charcoal"] as const;

const SIZES = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
  { width: 1440, height: 900 },
] as const;

/** Engine terms that must never appear in dealer-facing text on any walked state. */
const BANNED_ENGINE_TERMS: readonly RegExp[] = [
  /\bregistry default\b/i,
  /\bsurface's own registry\b/i,
  /\bidempotency\b/i,
  /\bthe engine reads no sales table\b/i,
  /\boffer to the accrual engine\b/i,
  /\bthrough the resolver\b/i,
  /\bsku_col\b/i,
  /\bcost_col\b/i,
  /\bretail_col\b/i,
  /\btrade_col\b/i,
  /\bmargin_floor_bps\b/i,
  /\brestocking_fee_bps\b/i,
  /\bcategory_policy\b/i,
  /\bcustomer_override\b/i,
  /\bPOS_SALE\b/i,
  /\bItem id\b/i,
];

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

/** The pricing view a walk stands on: the strip's pill at the desk widths, the phone's select below them. */
async function openPricingView(page: Page, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(".pr-tab").filter({ hasText: title }).first().click();
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

/**
 * Every pill, chip, button, table header, email and job-shape summary in a
 * table, and the number of lines it takes there (UX-106 round 7 + UX-118).
 *
 * The number of lines is the text's own line boxes: a range over the node's
 * contents gives one rect per line box, so the distinct tops are the lines.
 */
async function measureChips(page: Page, view: string): Promise<Chip[]> {
  return page.evaluate((name) => {
    const app = document.querySelector("dealer-admin-app");
    const root = app?.shadowRoot ?? document;
    const vw = window.innerWidth;
    const chips: { view: string; what: string; lines: number }[] = [];
    const lineCountOf = (el: Element): number => {
      const range = document.createRange();
      range.selectNodeContents(el);
      const tops = new Set<number>();
      for (const rect of Array.from(range.getClientRects())) {
        if (rect.width === 0 && rect.height === 0) continue;
        tops.add(Math.round(rect.top));
      }
      return Math.max(tops.size, 1);
    };

    for (const pill of Array.from(root.querySelectorAll("tr .chip, tr .btn, tr .badge-outline, tr .badge-status, tr .matrix-pill"))) {
      const cs = getComputedStyle(pill);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (pill.getBoundingClientRect().width === 0) continue;
      const row = pill.closest("tr");
      if (row === null) continue;
      const cell = pill.closest("td, th");
      const label = cell?.getAttribute("data-label") ?? "";
      const text = (pill.textContent ?? "").replace(/\s+/g, " ").trim();
      chips.push({
        view: name,
        what: `pill | ${row.getAttribute("data-lm") ?? row.getAttribute("data-profile") ?? row.getAttribute("data-entry-id") ?? row.className} | ${label === "" ? (cell?.className ?? "") : label} | ${text}`,
        lines: lineCountOf(pill),
      });
    }

    for (const th of Array.from(root.querySelectorAll("table thead th, table th[scope='col']"))) {
      const cs = getComputedStyle(th);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (th.getBoundingClientRect().width === 0) continue;
      const head = th.closest("thead");
      if (head !== null && getComputedStyle(head).display === "none") continue;
      const text = (th.textContent ?? "").replace(/\s+/g, " ").trim();
      if (text === "") continue;
      chips.push({
        view: name,
        what: `header | ${text}`,
        lines: lineCountOf(th),
      });
    }

    for (const email of Array.from(root.querySelectorAll(".staff-table .staff-email"))) {
      const cs = getComputedStyle(email);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (email.getBoundingClientRect().width === 0) continue;
      const text = (email.textContent ?? "").replace(/\s+/g, " ").trim();
      const lines = lineCountOf(email);
      if (vw > 900 && lines > 1) {
        chips.push({
          view: name,
          what: `staff-email | ${text}`,
          lines,
        });
      }
    }

    for (const desc of Array.from(root.querySelectorAll('[data-test="config-grid"] tbody th .dim'))) {
      const cs = getComputedStyle(desc);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (desc.getBoundingClientRect().width === 0) continue;
      const text = (desc.textContent ?? "").replace(/\s+/g, " ").trim();
      const lines = lineCountOf(desc);
      const maxAllowed = vw > 900 ? 2 : 4;
      if (lines > maxAllowed) {
        chips.push({
          view: name,
          what: `config-grid-desc | ${text}`,
          lines,
        });
      }
    }

    return chips;
  }, view);
}

/** Dealer-facing text audit: checks for banned engine words, raw SCREAMING_ENUMs, and bare basis-point figures on the active state. */
async function measureEngineWords(page: Page, view: string): Promise<string[]> {
  return page.evaluate(
    ({ name, patterns }) => {
      const app = document.querySelector("dealer-admin-app");
      const root = app?.shadowRoot ?? document;
      const work = root.querySelector(".surface-work");
      if (work === null) return [];
      const text = (work.textContent ?? "").replace(/\s+/g, " ");
      const hits: string[] = [];
      for (const src of patterns) {
        const rx = new RegExp(src, "i");
        const m = text.match(rx);
        if (m !== null) {
          hits.push(`${name} | engine term "${m[0]}"`);
        }
      }
      // Raw SCREAMING_ENUM (two or more upper-case words joined by an underscore) in visible text (UX-118 r2).
      const screamingRx = /\b[A-Z]{2,}(?:_[A-Z0-9]{2,})+\b/;
      const leaves = Array.from(work.querySelectorAll("*")).filter((el) => el.children.length === 0);
      for (const leaf of leaves) {
        if (leaf.tagName.toLowerCase() === "option") continue;
        if (name.startsWith("audit:") && leaf.closest('[data-test="audit-changes"], .audit-table td[data-label="Action"], [data-detail-for]') !== null) {
          continue;
        }
        const cs = getComputedStyle(leaf);
        if (cs.display === "none" || cs.visibility === "hidden") continue;
        if (leaf.getBoundingClientRect().width === 0) continue;
        const leafText = (leaf.textContent ?? "").replace(/\s+/g, " ").trim();
        const m = leafText.match(screamingRx);
        if (m !== null) {
          hits.push(`${name} | raw SCREAMING_ENUM "${m[0]}"`);
        }
      }
      // Bare four-digit basis-point figure with no unit beside a percent field (UX-118 r2).
      const pctLabelRx = /\b(margin|floor|target|markup|discount|rebate\s+rate|restocking\s+fee)\b/i;
      const bareBpsRx = /(?<![-\d.$])\b\d{4}\b(?!\s*%|[-\d.])/;
      for (const row of Array.from(work.querySelectorAll("tbody tr"))) {
        for (const cell of Array.from(row.querySelectorAll("td[data-label]"))) {
          const label = cell.getAttribute("data-label") ?? "";
          const val = (cell.textContent ?? "").replace(/\s+/g, " ").trim();
          if (pctLabelRx.test(label) && bareBpsRx.test(val)) {
            hits.push(`${name} | bare basis-point figure "${val}" in "${label}"`);
          }
        }
        const fieldCell = row.querySelector('td[data-label="Field"]');
        const changeCell = row.querySelector('td[data-label="Change"]');
        if (fieldCell !== null && changeCell !== null) {
          const fieldText = (fieldCell.textContent ?? "").replace(/\s+/g, " ").trim();
          const changeText = (changeCell.textContent ?? "").replace(/\s+/g, " ").trim();
          if (pctLabelRx.test(fieldText) && bareBpsRx.test(changeText)) {
            hits.push(`${name} | bare basis-point figure "${changeText}" beside "${fieldText}"`);
          }
        }
      }
      return hits;
    },
    { name: view, patterns: BANNED_ENGINE_TERMS.map((r) => r.source) },
  );
}

/** The page itself never scrolls sideways at a phone's width. */
async function pageScrolls(page: Page): Promise<{ scroll: number; client: number }> {
  return page.evaluate(() => ({ scroll: document.scrollingElement?.scrollWidth ?? 0, client: document.scrollingElement?.clientWidth ?? 0 }));
}

/**
 * The manifest: every state the walk visits, in the order it visits them.
 *
 * This is the walk's only source. A state that is added, removed or renamed
 * changes this list and nothing else, and the leg below fails when the walk
 * and the manifest disagree.
 */
const STATES: readonly State[] = [
  {
    id: "staff",
    tables: 1,
    open: async (page) => {
      await openSurface(page, "staff", '[data-test="staff-count"]');
    },
  },
  ...PRICING_VIEWS.map(
    (view): State => ({
      id: `pricing:${view.id}`,
      tables: view.tables,
      open: async (page) => {
        await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
        await openPricingView(page, view.title, view.settle);
        if (view.id === "customers") {
          // The customer prices table only lands once a customer is asked for.
          await page.locator('[data-picker-search="pr-cust-id"]').fill("Point Loma");
          await page.locator('[data-action="picker-choose"][data-picker-field="pr-cust-id"]').first().click();
          await page.locator('[data-action="pr-cust-load"]').click();
          await expect(page.locator('[data-test="pr-cust-count"]')).toBeVisible({ timeout: 30_000 });
          await page.waitForLoadState("networkidle");
          await waitForQuiet(page);
        }
      },
    }),
  ),
  {
    id: "audit:list",
    tables: 1,
    open: async (page) => {
      await openSurface(page, "audit", '[data-test="audit-count"]');
    },
  },
  {
    // The drawer opens over the list, so the state still carries the list's
    // table. The drawer's own change set is a JSON block, not a table.
    id: "audit:drawer",
    tables: 1,
    open: async (page) => {
      await openSurface(page, "audit", '[data-test="audit-count"]');
      await page.locator("tr[data-action='open-audit-entry']").first().click();
      await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await waitForQuiet(page);
    },
  },
  {
    id: "audit:trail",
    tables: 2,
    open: async (page) => {
      await openSurface(page, "audit", '[data-test="audit-count"]');
      await page.locator("tr[data-action='open-audit-entry']").first().click();
      await expect(page.locator("[data-detail-state='ready']").first()).toBeVisible({ timeout: 30_000 });
      await page.locator("[data-action='open-audit-trail']").first().click();
      await expect(page.locator("[data-test=audit-trail]").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      await waitForQuiet(page);
    },
  },
  {
    id: "roles:grid",
    tables: 2,
    open: async (page) => {
      await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
    },
  },
  {
    id: "hotkeys:rosters",
    tables: 0,
    open: async (page) => {
      await openSurface(page, "hotkeys", '[data-test^="hotkeys-roster-"]');
    },
  },
  {
    // The editors. A table behind a click is a table still: the margin
    // policy's own floor panel, the break schedule's tiers, one batch's
    // lines, the price file's diff and the curation editor's own rows.
    id: "pricing:margin-editor",
    tables: 1,
    open: async (page) => {
      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      await openPricingView(page, "Margin policy", '[data-test="pr-margin-count"]');
      await page.locator('[data-action="pr-policy-edit"]').first().click();
      await waitForQuiet(page);
    },
  },
  {
    id: "pricing:break-editor",
    tables: 2,
    open: async (page) => {
      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      await openPricingView(page, "Quantity breaks", '[data-test="pr-breaks-count"]');
      await page.locator('[data-action="pr-break-edit"]').first().click();
      await expect(page.locator('[data-field="pr-brk-item-0-from"]')).toBeVisible({ timeout: 30_000 });
      await waitForQuiet(page);
    },
  },
  {
    id: "pricing:batch-lines",
    tables: 3,
    open: async (page) => {
      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      await openPricingView(page, "Batches & imports", '[data-test="pr-profiles-count"]');
      await page.locator('[data-action="pr-batch-open"]').first().click();
      await waitForQuiet(page);
    },
  },
  {
    id: "pricing:pricefile-preview",
    tables: 3,
    open: async (page) => {
      await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
      await openPricingView(page, "Price file", '[data-test="pr-pf-runs-count"]');
      await page.locator('[data-field="pr-pf-vendor"]').selectOption({ label: "Northline Supply" });
      await page
        .locator('[data-field="pr-pf-file"]')
        .setInputFiles({ name: "price-file.csv", mimeType: "text/csv", buffer: Buffer.from("sku,retail,cost\nPAV-1,480,300\nPAV-2,510,320\nPAV-3,530,340\n") });
      await page.locator('[data-action="pr-pf-preview"]').click();
      await expect(page.locator('[data-test="pr-pf-preview"]')).toBeVisible({ timeout: 30_000 });
      await waitForQuiet(page);
    },
  },
  {
    id: "roles:editor",
    tables: 2,
    open: async (page) => {
      await openSurface(page, "role-config", '[data-test="role-matrix-card"]');
      // UX-119c: every job shape opens first, so the sweep measures every tile.
      await page.locator('[data-test="config-toggle-all"]').click();
      await page.locator('[data-action="open-config-editor"]').first().click();
      await waitForQuiet(page);
    },
  },
];

// -- the sweep ---------------------------------------------------------------------------------------

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`no table on any view scrolls sideways or wraps a pill or header at ${size.width}, ${theme}`, async ({ page }) => {
      test.setTimeout(300_000);
      await page.setViewportSize({ width: size.width, height: size.height });
      await signIn(page);
      if (theme === "charcoal") await goCharcoal(page);

      const boxes: Box[] = [];
      const chips: Chip[] = [];
      const engineHits: string[] = [];
      const visited: string[] = [];

      for (const state of STATES) {
        await state.open(page);
        visited.push(state.id);
        boxes.push(...(await measure(page, state.id)));
        chips.push(...(await measureChips(page, state.id)));
        engineHits.push(...(await measureEngineWords(page, state.id)));
      }

      // The walk visited the manifest's states, exactly, once each. A state
      // that stopped rendering fails here instead of quietly shrinking the
      // measurement, and a state that was walked twice fails here too.
      expect([...visited].sort(), "the sweep did not walk exactly the manifest's states").toEqual(STATES.map((state) => state.id).sort());

      // Each state carries its own tables. A table that stopped rendering
      // fails the state it stopped in, and a table that appeared fails the
      // state it appeared in: the failure names the state that changed.
      for (const state of STATES) {
        const tables = boxes.filter((box) => box.view === state.id && box.kind === "table").length;
        expect(tables, `the state ${state.id} does not carry its own tables`).toBe(state.tables);
      }

      // The page itself never scrolls sideways.
      const scrolled = await pageScrolls(page);
      expect(scrolled.scroll, `the page scrolls sideways at ${size.width}`).toBeLessThanOrEqual(scrolled.client);

      // Every box: the scrollWidth against the clientWidth. A value that reads
      // in the label column is the other way round: it sits before the column
      // it belongs in.
      const cut = boxes.filter((b) => (b.kind === "stray" ? b.scroll < b.client : b.scroll > b.client + 1));
      const report = cut.map((b) => `  ${b.view} | ${b.what} | scroll ${b.scroll} client ${b.client}`).join("\n");
      expect.soft(cut, `these boxes cut their content at ${size.width} (${theme}):\n${report}`).toEqual([]);

      // Every pill, chip, button, table header, staff email and job-shape
      // description stays within its line budget.
      const wrapped = chips.filter((chip) => chip.lines > 1);
      const chipReport = wrapped.map((chip) => `  ${chip.view} | ${chip.what} | ${chip.lines} lines`).join("\n");
      expect.soft(wrapped, `these pills, headers or cells wrap at ${size.width} (${theme}):\n${chipReport}`).toEqual([]);

      // No engine words on any dealer screen (UX-118 item 5).
      expect.soft(engineHits, `engine terms found at ${size.width} (${theme}):\n${engineHits.join("\n")}`).toEqual([]);
    });
  }
}
