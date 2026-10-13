// Gap 10 of the per role homes: the trend and gauge shapes draw a sparkline
// and a segmented meter from the server's points, beside the figure and with
// every exact number as text. Tokens only, nothing animates.
import { expect, test } from "@playwright/test";
import { managerHandoff, resetStub, signInByCard } from "./helpers";

test("the sales manager's trend is a column per point, with a spoken label and its ends as text", async ({ request, page }) => {
  await resetStub(request);
  await signInByCard(page, "sales-manager");
  const card = page.locator("[data-widget='sales.trend']");
  await expect(card.locator(".figure")).toHaveText("$1,410.00");
  const spark = card.locator(".spark");
  await expect(spark).toHaveAttribute("role", "img");
  await expect(spark).toHaveAttribute("aria-label", "Trend from Day 1, $1,020.00, to Day 7, $1,410.00");
  await expect(spark.locator(".spark-col")).toHaveCount(7);
  await expect(card.locator(".spark-ends")).toHaveText("Day 1Day 7");
  // The tallest column is the last (141 against the rest) and the bars are the sheet's data ink, not a raw colour.
  const heights = await spark.locator(".spark-col").evaluateAll((nodes) => nodes.map((n) => n.getBoundingClientRect().height));
  expect(heights[6]).toBe(Math.max(...heights));
  expect(heights[6]).toBeGreaterThan(heights[2]);
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

test("nothing on a trend or a gauge animates, so reduced motion has nothing to reduce", async ({ request, page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await resetStub(request);
  await signInByCard(page, "sales-manager");
  const moving = await page.locator("[data-widget='sales.trend'] .spark-col, [data-widget='sales.trend'] .spark").evaluateAll((nodes) =>
    nodes.filter((n) => {
      const s = getComputedStyle(n);
      return s.animationName !== "none" || Number.parseFloat(s.transitionDuration) > 0;
    }).length,
  );
  expect(moving).toBe(0);
});
