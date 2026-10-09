// Manager Home, sub item 4: the UI notes (MANAGER-HOME-UI-NOTES). The two
// new needs links name views this app owns; receiving differences open
// Receiving, not Yard Pick; the receiving difference definition is said in
// words where it helps; the rows the board drew that nothing measures are
// said plainly, never faked.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("receiving differences open Receiving, this app's own view, not Yard Pick", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const row = page.locator("[data-needs='purchasing.receiving_differences']");
  await expect(row.locator(".view-words")).toHaveText("Receiving · short");
  await row.getByRole("button", { name: "Settle" }).click();
  await expect(page.getByRole("heading", { name: "Receiving · short" })).toBeVisible();
  // The view belongs to this app: its back control returns to the landing,
  // never to another surface.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.locator("[data-widget='hero.deliveries']")).toBeVisible();
});

test("the receiving difference definition is said in words, and only the differences are listed", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Receiving · short" }).click();
  const note = page.locator("[data-test='receiving-definition']");
  await expect(note).toContainText("landed part of what was ordered");
  await expect(note).toContainText("leaves this list when the rest lands or the order is closed short");
  const cards = page.locator("[data-po]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first().locator(".order")).toHaveText("PO-TRT-77");
  await expect(cards.first().locator(".chip")).toContainText("Partial");
  await expect(cards.first().locator(".detail").first()).toContainText("1 line still short");
  // Cost, visible: the order's still-open value, the core's integer cents formatted for a person.
  await expect(cards.first().locator(".detail .amount")).toHaveText("$54.00");
  // The worklist's not-short orders are said as a count, not hidden and not listed as differences.
  await expect(page.locator(".count-line")).toContainText("1 purchase order on the dock landed everything ordered");
});

test("an empty receiving dock says so in words, never as a zero", async ({ request, page }) => {
  await resetStub(request);
  await useScenario(request, "no-differences");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Receiving · short" }).click();
  await expect(page.locator("[data-po]")).toHaveCount(0);
  await expect(page.locator(".list-card .notice-line")).toHaveText("No purchase order landed short at this branch.");
});

test("the needs links' words are the core's own view strings, never reworded", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-needs='tasks.open_shortages'] .view-words")).toHaveText("Shortages · open");
  await expect(page.locator("[data-needs='tasks.stale_picks'] p.view-words")).toHaveText("Picks · past deadline");
  await expect(page.locator("[data-needs='purchasing.receiving_differences'] .view-words")).toHaveText("Receiving · short");
});

test("the rows nothing measures carry their no-source sentence and no number", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const unsourced = page.locator("[data-unsourced='weights.pending']");
  await expect(unsourced).toHaveCount(1);
  await expect(unsourced.locator(".what")).toHaveText("Weights pending");
  await expect(unsourced.locator(".words")).toContainText("No source is wired for this row yet");
  await expect(unsourced.locator(".needs-count")).toHaveCount(0);
  await expect(unsourced.getByRole("button")).toHaveCount(0);
  const tile = page.locator(".who-may").filter({ hasText: "Unread messages" });
  await expect(tile).toContainText("No measure is wired for this tile in the widget catalog yet");
});
