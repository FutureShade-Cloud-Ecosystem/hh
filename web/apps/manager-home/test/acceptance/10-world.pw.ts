// The world leg: the card lands on the yard Manager Home, and the queue
// counts the seed. L-card (the yard manager's own card is the way in),
// L-branch (the queue counts Point Loma, the one branch the grant reaches), and
// the profile's widgets in the profile's order on the real core's rows.
import { test, expect } from "@playwright/test";
import { mint, staffGet } from "./helpers.js";

interface NeedsRowWire {
  source_key?: string;
  count?: number;
}

/** The caller's own needs queue as the core serves it: the rows by source key, and the total. */
async function needsFromCore(page: import("@playwright/test").Page): Promise<{ counts: Map<string, number>; total: number }> {
  const bearer = await mint(page, "yard-manager");
  const answer = await staffGet(page, bearer, "/api/v1/dashboard/needs");
  expect(answer.status).toBe(200);
  const body = answer.body as { rows?: NeedsRowWire[]; total?: number };
  const counts = new Map<string, number>();
  for (const row of body.rows ?? []) {
    if (typeof row.source_key === "string") counts.set(row.source_key, row.count ?? 0);
  }
  return { counts, total: body.total ?? 0 };
}

/** The caller's weights tile as the core's home serves it, or undefined when the home carries none. */
async function weightsTileFromCore(page: import("@playwright/test").Page): Promise<{ state: string; number: number | null } | undefined> {
  const bearer = await mint(page, "yard-manager");
  const answer = await staffGet(page, bearer, "/api/v1/dashboard");
  expect(answer.status).toBe(200);
  const tiles = ((answer.body as { tiles?: Array<{ tile_id?: string; state?: string; number?: number }> }).tiles ?? []);
  const tile = tiles.find((t) => t.tile_id === "weight.pending");
  return tile === undefined ? undefined : { state: tile.state ?? "", number: typeof tile.number === "number" ? tile.number : null };
}

test("the yard manager's card is the way in, and the landing is the profile's rows", async ({ page }) => {
  await page.goto("/home/");
  const card = page.locator("[data-flow='yard-manager']");
  await expect(card).toBeVisible();
  await card.click();
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await band.locator("button.account").click();
  await expect(page.getByTestId("header-word")).toHaveText("Yard manager");
  const widgets = page.locator("[data-widget]");
  await expect(widgets).toHaveCount(8);
  const keys = await widgets.evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(keys).toEqual([
    "hero.deliveries",
    "needs.you",
    "picks.board",
    "deliveries.branch",
    "picks.gauge",
    "orders.open",
    "warranty.open",
    "messages.unread",
  ]);
});

test("needs you counts the seed: two open shortages, one receiving difference, and the stale picks the world's clock has aged", async ({ page }) => {
  const core = await needsFromCore(page);
  const weights = await weightsTileFromCore(page);
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
  const needs = page.locator("[data-widget='needs.you']");
  // The seed plants exactly two unresolved shortages at Point Loma (SH1 and SH2) and one PARTIAL
  // purchase order with a short line.
  await expect(needs.locator("[data-needs='tasks.open_shortages'] .needs-count")).toHaveText("2");
  await expect(needs.locator("[data-needs='purchasing.receiving_differences'] .needs-count")).toHaveText("1");
  expect(core.counts.get("tasks.open_shortages")).toBe(2);
  expect(core.counts.get("purchasing.receiving_differences")).toBe(1);
  // A stale pick is a pick past its deadline, and the seeded Point Loma picks age into it as the
  // world's clock moves. So the leg reads what the core counts for this caller and holds the
  // widget to it: the same count on the row, and no row at all when the core counts none (a
  // count of zero is absent by design, never shown).
  // A pick can cross its deadline between the core read above and the page's own read, so the
  // screen's count is held to the core's count read on either side of it.
  const staleBefore = core.counts.get("tasks.stale_picks") ?? 0;
  const staleRow = needs.locator("[data-needs='tasks.stale_picks']");
  const shown = (await staleRow.count()) === 0 ? 0 : Number(await staleRow.locator(".needs-count").textContent());
  const staleAfter = (await needsFromCore(page)).counts.get("tasks.stale_picks") ?? 0;
  expect([staleBefore, staleAfter], "the stale picks row shows the core's count (absent when none)").toContain(shown);
  // The widget's total is the core's total, with the stale count the screen shows, plus the weights
  // row the app joins when the core measures it (weights-logic.ts mergeWeights).
  const weighed = weights?.state === "ok" && weights.number !== null && weights.number > 0 ? weights.number : 0;
  await expect(needs.locator(".needs-total")).toHaveText(new RegExp(`^${core.total - staleBefore + shown + weighed} waiting · `));
  // The hero carries a measured figure for the dealer's day, never a dash.
  await expect(page.locator("[data-widget='hero.deliveries'] .figure")).not.toHaveText("-");
});

test("the weights row follows the core's own answer: the measured count, or the words row with no number", async ({ page }) => {
  // The core measures Weights pending now (the weight.pending tile on the caller's home). The app
  // joins a real count when the tile answers a count, shows the words row only when the tile is
  // unwired or errors, and shows nothing for a tile that is hidden or answers none.
  const tile = await weightsTileFromCore(page);
  expect(tile, "the caller's home carries the weights tile").toBeDefined();
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
  const needs = page.locator("[data-widget='needs.you']");
  const measured = needs.locator("[data-needs='weights.pending']");
  const unsourced = needs.locator("[data-unsourced='weights.pending']");
  // The app's own mapping (weights-api.ts): ok, unwired, error and hidden are read as given, and any
  // other state is read as error, so it is the words row too.
  if (tile?.state === "ok") {
    if (tile.number !== null && tile.number > 0) {
      await expect(measured).toHaveCount(1);
      await expect(measured.locator(".needs-count")).toHaveText(String(tile.number));
    } else {
      await expect(measured).toHaveCount(0);
    }
    await expect(unsourced).toHaveCount(0);
  } else if (tile?.state === "hidden") {
    await expect(measured).toHaveCount(0);
    await expect(unsourced).toHaveCount(0);
  } else {
    await expect(unsourced).toHaveCount(1);
    await expect(unsourced.locator(".needs-count")).toHaveCount(0);
    await expect(measured).toHaveCount(0);
  }
});
