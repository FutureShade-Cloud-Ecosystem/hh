// Gap 10 of the per role homes: the gauge shape draws a segmented meter from
// the server's points, beside the figure and with every exact number as text.
// Tokens only, nothing animates. A trend stays its figure (review P2-3): the
// core measures it over the metric's first dimension, which is the branch.
import { expect, test } from "@playwright/test";
import { managerHandoff, resetStub, signInByCard } from "./helpers";

test("the sales manager's trend stays its figure: the core's points are one per branch, not a time series", async ({ request, page }) => {
  await resetStub(request);
  await signInByCard(page, "sales-manager");
  const card = page.locator("[data-widget='sales.trend']");
  await expect(card.locator(".figure")).toHaveText("$1,000.00");
  await expect(card.locator(".spark")).toHaveCount(0);
  await expect(card.getByText("Trend from")).toHaveCount(0);
});

test("the yard manager's gauge is one meter of segments with a legend carrying every figure", async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const card = page.locator("[data-widget='picks.gauge']");
  await expect(card.locator(".figure")).toHaveText("20");
  await expect(card.locator(".meter .seg")).toHaveCount(4);
  await expect(card.locator(".meter")).toHaveAttribute("aria-label", "Queue depth: Waiting 8, Claimed 5, Picked 3, Staged 4");
  const legend = card.locator(".legend-row");
  await expect(legend).toHaveCount(4);
  await expect(legend.nth(0)).toHaveText("Waiting8");
  await expect(card.locator(".seg.tone-good")).toHaveCount(1);
  // No ceiling invented: the meter shows composition, so the segments together fill the track.
  const widths = await card.locator(".meter .seg").evaluateAll((nodes) => nodes.map((n) => n.getBoundingClientRect().width));
  const track = await card.locator(".meter").evaluate((n) => n.getBoundingClientRect().width);
  expect(widths.reduce((a, b) => a + b, 0)).toBeGreaterThan(track * 0.85);
});

test("nothing on a gauge animates, so reduced motion has nothing to reduce", async ({ request, page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await resetStub(request);
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-widget='picks.gauge'] .meter")).toBeVisible();
  const moving = await page.locator("[data-widget='picks.gauge'] .seg, [data-widget='picks.gauge'] .meter").evaluateAll((nodes) =>
    nodes.filter((n) => {
      const s = getComputedStyle(n);
      return s.animationName !== "none" || Number.parseFloat(s.transitionDuration) > 0;
    }).length,
  );
  expect(moving).toBe(0);
});
