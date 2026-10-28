// UX-106 round 5: the audit list fits a phone.
//
// The defect. The audit trail's list is a five column table, and at 390 it
// scrolled sideways under a cut column: the Entity column ran off the card's
// right edge, so a dealer read half a handle and no entity type at all. The
// one entity's trail table, on the same page, cut its Actor column the same
// way. It is the same defect the margin policy table had, and it gets the same
// house answer: under 900 px each entry is a card row of labelled pairs.
//
// The legs:
//
//   1. the audit list does not scroll sideways at 390, and its rows are card
//      rows: the head is gone and each row lays out as a grid;
//   2. the one entity's trail does not scroll sideways at 390 either;
//   3. a card row still opens the entry's drawer, by pointer and by keyboard;
//   4. the desk keeps the table.
//
// The measurement is the one the defect names: the list's scrollWidth against
// its clientWidth. A table that scrolls sideways has scrollWidth greater than
// clientWidth; a card row has them equal.
import { expect, test, type Locator, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
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

/** One entry's drawer, once its own read has landed. */
async function openDrawer(page: Page): Promise<Locator> {
  await page.locator("tr[data-action='open-audit-entry']").first().click();
  const drawer = page.locator("[data-detail-state='ready']").first();
  await expect(drawer).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  return drawer;
}

/** One entity's whole trail, once its own read has landed. */
async function openTrail(page: Page): Promise<Locator> {
  await openDrawer(page);
  await page.locator("[data-action='open-audit-trail']").first().click();
  const trail = page.locator("[data-test=audit-trail]").first();
  await expect(trail).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  return trail;
}

/** How far a box scrolls sideways: its scrollWidth against its clientWidth. */
async function sideways(locator: Locator): Promise<{ scroll: number; client: number }> {
  return locator.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
}

/** How a box lays itself out: the display its own rule gives it. */
async function display(locator: Locator): Promise<string> {
  return locator.evaluate((el) => getComputedStyle(el).display);
}

// -- 1: the audit list fits a phone --------------------------------------------------------------------

test("the audit list does not scroll sideways at 390, and its rows are card rows", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);

  const wrap = page.locator(".tablewrap").first();
  const state = await sideways(wrap);
  expect(state.scroll, "the audit list scrolls sideways under a cut column at 390").toBeLessThanOrEqual(state.client);

  // The card row: the table's head is gone and each row lays out as a grid.
  const table = page.locator("table").first();
  expect(await display(table.locator("thead").first()), "the audit list still shows a table head at 390").toBe("none");
  const row = page.locator("tr[data-action='open-audit-entry']").first();
  expect(await display(row), "the audit list's row is not a card row at 390").toBe("grid");

  // Every cell of the card row names itself, so the pair reads without a head.
  const labels = await row.locator("td").evaluateAll((cells) => cells.map((cell) => cell.getAttribute("data-label") ?? ""));
  expect(labels, "the card row does not label its pairs").toEqual(["When", "Action", "Entity", "Actor", "Request"]);
});

// -- 2: the one entity's trail fits a phone ------------------------------------------------------------

test("the one entity's trail does not scroll sideways at 390", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);
  const trail = await openTrail(page);

  const wrap = trail.locator(".tablewrap").first();
  const state = await sideways(wrap);
  expect(state.scroll, "the entity's trail scrolls sideways under a cut column at 390").toBeLessThanOrEqual(state.client);

  expect(await display(trail.locator("thead").first()), "the entity's trail still shows a table head at 390").toBe("none");
  const row = trail.locator("tr[data-action='open-audit-entry']").first();
  expect(await display(row), "the entity's trail row is not a card row at 390").toBe("grid");

  const labels = await row.locator("td").evaluateAll((cells) => cells.map((cell) => cell.getAttribute("data-label") ?? ""));
  expect(labels, "the trail's card row does not label its pairs").toEqual(["When", "Action", "Actor"]);
});

// -- 3: a card row still opens the drawer ---------------------------------------------------------------

test("a card row opens the entry's drawer by pointer", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);

  const row = page.locator("tr[data-action='open-audit-entry']").first();
  await row.click();
  const drawer = page.locator("[data-detail-state='ready']").first();
  await expect(drawer, "the card row did not open the entry's drawer").toBeVisible({ timeout: 30_000 });
});

test("a card row opens the entry's drawer by keyboard", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);

  const row = page.locator("tr[data-action='open-audit-entry']").first();
  await row.focus();
  await page.keyboard.press("Enter");
  const drawer = page.locator("[data-detail-state='ready']").first();
  await expect(drawer, "the keyboard did not open the entry's drawer from the card row").toBeVisible({ timeout: 30_000 });
});

// -- 4: the desk keeps the table ------------------------------------------------------------------------

test("the desk keeps the audit list a table", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openAudit(page);

  const table = page.locator("table").first();
  expect(await display(table.locator("thead").first()), "the desk lost the audit list's table head").not.toBe("none");
  const row = page.locator("tr[data-action='open-audit-entry']").first();
  expect(await display(row), "the desk's audit list row is not a table row").toBe("table-row");

  const state = await sideways(page.locator(".tablewrap").first());
  expect(state.scroll, "the audit list scrolls sideways at the desk's width").toBeLessThanOrEqual(state.client);
});
