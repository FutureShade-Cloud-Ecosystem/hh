// The world leg: the card lands on the yard Manager Home, and the queue
// counts the seed. L-card (the yard manager's own card is the way in),
// L-branch (the queue counts Trenton, the one branch the grant reaches), and
// the profile's widgets in the profile's order on the real core's rows.
import { test, expect } from "@playwright/test";

test("the yard manager's card is the way in, and the landing is the profile's rows", async ({ page }) => {
  await page.goto("/home/");
  const card = page.locator("[data-flow='yard-manager']");
  await expect(card).toBeVisible();
  await card.click();
  await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "");
  await expect(page.getByTestId("header-word")).toHaveText("Yard manager");
  const widgets = page.locator("[data-widget]");
  await expect(widgets).toHaveCount(7);
  const keys = await widgets.evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(keys).toEqual([
    "hero.deliveries",
    "needs.you",
    "picks.board",
    "deliveries.branch",
    "picks.gauge",
    "orders.open",
    "warranty.open",
  ]);
});

test("needs you counts the seed: two open shortages, one receiving difference, no stale pick at this branch", async ({ page }) => {
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
  const needs = page.locator("[data-widget='needs.you']");
  // The seed plants exactly two unresolved shortages at Trenton (SH1 and SH2) and one PARTIAL
  // purchase order with a short line; the stale picks sit at the other branch, out of this
  // grant's reach, so their row is absent (a count of zero is absent by design, never shown).
  await expect(needs.locator("[data-needs='tasks.open_shortages'] .needs-count")).toHaveText("2");
  await expect(needs.locator("[data-needs='purchasing.receiving_differences'] .needs-count")).toHaveText("1");
  await expect(needs.locator("[data-needs='tasks.stale_picks']")).toHaveCount(0);
  await expect(needs.locator(".needs-total")).toHaveText("3 waiting · Watch");
  // The hero carries a measured figure for the dealer's day, never a dash.
  await expect(page.locator("[data-widget='hero.deliveries'] .figure")).not.toHaveText("-");
});

test("the board's unsourced row is said plainly and carries no number", async ({ page }) => {
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
  const unsourced = page.locator("[data-unsourced='weights.pending']");
  await expect(unsourced).toHaveCount(1);
  await expect(unsourced.locator(".needs-count")).toHaveCount(0);
});
