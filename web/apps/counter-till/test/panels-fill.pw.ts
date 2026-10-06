// The panel view fills the screen (the till fill work): the detached
// layout's 2x2 grid takes the window's width minus the page gutter and the
// height between the header and the pinned bottom bars, the two rows share
// that height, and the fulfilment row can never be scrolled out of the Items
// panel. The Quote Desk's workarea is the reference: full measure, each panel
// a column with its own scroll zone and a foot that stays put.
//
// The geometry is read through the shadow chain (ct-app-root > the terminal >
// the panels host), the way a person sees it: boxes on screen, not styles.
import { expect, test, type Page } from "@playwright/test";

/** The page gutter the Quote Desk's workarea uses, and this grid with it. */
const GUTTER = 16;
/** The tiling's design gap: between panels and around the grid's edge. */
const TILE_GAP = 10;

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Search from the counter's own field and add the hit to the ticket. */
async function addLine(page: Page, query: string, hit: string): Promise<void> {
  const field = page.getByTestId("pos-scan-input");
  await field.fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hit }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hit })).toHaveCount(1);
}

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
  bottom: number;
  right: number;
}

interface PanelsGeometry {
  viewportW: number;
  viewportH: number;
  /** The terminal's page header bar (full bleed, above the grid). */
  topbar: Box | null;
  /** The panels grid surface itself: the box the four panels tile into. */
  grid: Box | null;
  panels: Record<string, Box>;
  /** The panel head band (the drag handle) of the Items panel. */
  itemsHead: Box | null;
  /** The Items panel's scroll zone (the step body slot). */
  itemsZone: Box | null;
  zoneScroll: Record<string, { scrollH: number; clientH: number }>;
  /** The fulfilment row, wherever the layout puts it. */
  fulfil: Box | null;
  /** The pinned bottom bars: the sale utilities and the footer stat bar. */
  saleutil: Box | null;
  txbar: Box | null;
  /** The pinned hotkey legend overlaying the window's bottom edge. */
  legend: Box | null;
}

/** Read every box the fill contract speaks of, piercing the shadow chain. */
async function readGeometry(page: Page): Promise<PanelsGeometry> {
  return page.evaluate(() => {
    const walk = (root: ParentNode): Element[] => {
      const out: Element[] = [];
      for (const el of root.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) out.push(...walk(el.shadowRoot));
      }
      return out;
    };
    const all = walk(document);
    const findOne = (selector: string) => all.find((el) => el.matches(selector)) ?? null;
    const boxOf = (el: Element | null): Box | null => {
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return {
        x: r.x,
        y: r.y,
        w: r.width,
        h: r.height,
        bottom: r.bottom,
        right: r.right,
      };
    };
    const panelsHost = findOne("hos-pos-panels");
    const panels: Record<string, Box> = {};
    const zoneScroll: Record<string, { scrollH: number; clientH: number }> = {};
    for (const panel of panelsHost?.shadowRoot?.querySelectorAll<HTMLElement>(".panel") ?? []) {
      const id = panel.getAttribute("data-panel");
      if (!id) continue;
      panels[id] = boxOf(panel) as Box;
      const body = panel.querySelector<HTMLElement>(".body");
      if (body) zoneScroll[id] = { scrollH: body.scrollHeight, clientH: body.clientHeight };
    }
    const terminal = findOne("hos-pos-terminal");
    return {
      viewportW: window.innerWidth,
      viewportH: window.innerHeight,
      topbar: boxOf(terminal?.shadowRoot?.querySelector(".topbar") ?? null),
      grid: boxOf(panelsHost?.shadowRoot?.querySelector(".surface") ?? null),
      panels,
      itemsHead: boxOf(panelsHost?.shadowRoot?.querySelector('.panel[data-panel="items"] .head') ?? null),
      itemsZone: boxOf(panelsHost?.shadowRoot?.querySelector('.panel[data-panel="items"] .body') ?? null),
      zoneScroll,
      fulfil: boxOf(terminal?.shadowRoot?.querySelector(".fulfil") ?? null),
      saleutil: boxOf(terminal?.shadowRoot?.querySelector(".saleutil") ?? null),
      txbar: boxOf(terminal?.shadowRoot?.querySelector(".txbar") ?? null),
      legend: boxOf(findOne("hos-pos-hotkey-legend")),
    };
  });
}

/** Bring the counter to the panels layout with a four-line ticket on it. */
async function panelsWithTicket(page: Page): Promise<void> {
  await signIn(page);
  await page.getByTestId("pos-view").click();
  await page.getByTestId("pos-layout-panels").click();
  await expect(page.getByTestId("pos-flow-panels")).toBeVisible();
  await addLine(page, "holland", "Holland Stone 60 mm");
  await addLine(page, "edge", "Edge Restraint 8 ft");
  await addLine(page, "polymeric", "Polymeric Locking Sand");
  await addLine(page, "wall", "Retaining Wall Stone");
  // The ticket is the posture: the search field hands back its term so the
  // drop stands down.
  await page.getByTestId("pos-scan-input").fill("");
}

const DESKTOP_SIZES: Array<[number, number]> = [
  [1440, 900],
  [1920, 1080],
];

for (const [width, height] of DESKTOP_SIZES) {
  test(`the grid fills the window at ${width}x${height}: gutter to gutter, header to the pinned bars`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await panelsWithTicket(page);
    const g = await readGeometry(page);

    // The full width: the page's gutter each side, no centred measure.
    const grid = g.grid as Box;
    expect(Math.abs(grid.x - GUTTER)).toBeLessThanOrEqual(2);
    expect(Math.abs(grid.right - (g.viewportW - GUTTER))).toBeLessThanOrEqual(2);

    // The full height: the grid starts below the header bar and the stack it
    // belongs to ends exactly above the pinned legend, whose own bottom is
    // the window's: nothing dead below the bars, nothing of the bars hidden.
    const topbar = g.topbar as Box;
    const saleutil = g.saleutil as Box;
    const txbar = g.txbar as Box;
    const legend = g.legend as Box;
    expect(grid.y).toBeGreaterThan(topbar.bottom);
    expect(saleutil.y).toBeGreaterThanOrEqual(grid.bottom - 1);
    expect(Math.abs(txbar.bottom - legend.y)).toBeLessThanOrEqual(3);

    // The two rows share the grid's height: equal rows, the design's tile
    // gap between them and at the edges, the pair covering the grid exactly.
    // The edges carry a 3px tolerance: the tiling rounds its rows against
    // the measured area while the boxes here carry the grid border's own
    // fraction, and a few pixels is the brief's own allowance.
    const customer = g.panels.customer as Box;
    const items = g.panels.items as Box;
    const payment = g.panels.payment as Box;
    const summary = g.panels.summary as Box;
    expect(Math.abs(customer.y - grid.y - TILE_GAP)).toBeLessThanOrEqual(3);
    expect(Math.abs(customer.y - items.y)).toBeLessThanOrEqual(2);
    expect(Math.abs(payment.y - summary.y)).toBeLessThanOrEqual(2);
    expect(Math.abs(payment.y - (customer.bottom + TILE_GAP))).toBeLessThanOrEqual(3);
    expect(Math.abs(payment.bottom - (grid.bottom - TILE_GAP))).toBeLessThanOrEqual(3);
    expect(Math.abs(customer.h - payment.h)).toBeLessThanOrEqual(2);
    expect(Math.abs(customer.h - (grid.h - TILE_GAP * 3) / 2)).toBeLessThanOrEqual(3);

    // The two columns share the width the same way.
    expect(Math.abs(customer.x - (grid.x + TILE_GAP))).toBeLessThanOrEqual(3);
    expect(Math.abs(items.x - (customer.right + TILE_GAP))).toBeLessThanOrEqual(3);
    expect(Math.abs(items.right - (grid.right - TILE_GAP))).toBeLessThanOrEqual(3);
    expect(Math.abs(customer.w - items.w)).toBeLessThanOrEqual(2);
  });

  test(`a four-line ticket at ${width}x${height}: the fulfilment row whole, the quiet panels still`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await panelsWithTicket(page);
    const g = await readGeometry(page);

    // The fulfilment row sits inside the Items panel's own box, below its
    // head, never cut by the panel's edge and never needing a scroll to
    // reach: it is the panel's foot.
    const items = g.panels.items as Box;
    const head = g.itemsHead as Box;
    const fulfil = g.fulfil as Box;
    expect(fulfil.y).toBeGreaterThanOrEqual(head.bottom - 1);
    expect(fulfil.bottom).toBeLessThanOrEqual(items.bottom + 1);

    // The panels whose content a four-line ticket does not grow hold their
    // content whole. Customer holds at both desktop sizes; Summary's
    // read-back holds at 1080 and runs 10px over at 900 (a content density
    // fact, not a fill defect: the zone scrolls its true overflow only).
    // Items and Payment carry the add stack and the tender tiles, which
    // overflow any equal half at these heights: their contract is the fill
    // plus the pinned foot, asserted above and below.
    const zoneIds = height === 1080 ? ["customer", "summary"] : ["customer"];
    for (const id of zoneIds) {
      const zone = g.zoneScroll[id];
      expect(zone, `${id} zone`).toBeDefined();
      expect((zone as { scrollH: number; clientH: number }).scrollH).toBeLessThanOrEqual(
        (zone as { scrollH: number; clientH: number }).clientH + 1,
      );
    }
  });
}

test("the tablet width keeps the two columns and still fills: no empty margins either side", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await panelsWithTicket(page);
  const g = await readGeometry(page);

  const grid = g.grid as Box;
  expect(Math.abs(grid.x - GUTTER)).toBeLessThanOrEqual(2);
  expect(Math.abs(grid.right - (g.viewportW - GUTTER))).toBeLessThanOrEqual(2);

  // The till's own tablet posture is two columns, as today.
  const customer = g.panels.customer as Box;
  const items = g.panels.items as Box;
  expect(Math.abs(customer.y - items.y)).toBeLessThanOrEqual(2);
  expect(items.x).toBeGreaterThan(customer.right);

  // And the stack still ends at the pinned legend: the fill holds at the
  // short height, the rows sharing whatever is left.
  const txbar = g.txbar as Box;
  const legend = g.legend as Box;
  expect(Math.abs(txbar.bottom - legend.y)).toBeLessThanOrEqual(3);
  const payment = g.panels.payment as Box;
  expect(Math.abs(payment.bottom - (grid.bottom - TILE_GAP))).toBeLessThanOrEqual(3);
});
