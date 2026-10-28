// UX-106 round 6: the pricing audit log, and a sweep of every table.
//
// The defect. The PRICING AUDIT log's table is six columns (When, Entity,
// Action, Field, Change, Actor), and at 390 it scrolled sideways: its
// scrollWidth was 578 in a 316 px card. It is the same defect the margin
// policy table had in round 1 and the audit list had in round 5, and it gets
// the same house answer: the card-row pattern.
//
// The sweep. The round's point was that no cut table is left for a later
// round, so the round measured every table in every view the brief lists, in
// both themes, and recorded each one.
//
// The sweep itself moved to round 7's file (test/ux-106-r7.pw.ts): there the
// walk runs off ONE state manifest, every state says how many tables it must
// carry, and the walk's guards name the state that changed (round 7's P3).
// This file keeps the leg the round named: the pricing audit log fits a phone.
import { expect, test, type Locator, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
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
