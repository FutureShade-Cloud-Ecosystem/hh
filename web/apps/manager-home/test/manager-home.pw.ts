// Manager Home, sub item 1: the landing from the caller's dashboard profile.
// The app boots in demo mode against the stub, the yard manager's card is
// the way in, and the bento renders the profile's widgets in the profile's
// order with the needs-you queue inside it. The same code reads the sales
// manager's profile and renders a different landing: one app, configured per
// role (decision 5), never code per role.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the yard manager's card signs in and the header names the role", async ({ page }) => {
  await page.goto("/");
  const card = page.locator("[data-flow='yard-manager']");
  await expect(card).toBeVisible();
  await card.click();
  await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "");
  await expect(page.getByTestId("header-word")).toHaveText("Yard manager");
});

test("the landing is the profile's widgets in the profile's order", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
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

test("the hero carries the day's count and its window word", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const hero = page.locator("[data-widget='hero.deliveries']");
  await expect(hero).toBeVisible();
  await expect(hero.locator(".figure")).toHaveText("14");
  await expect(hero.locator(".card-sub")).toHaveText("Today");
  await expect(hero.locator(".card-title")).toHaveText("Deliveries today");
});

test("the needs-you queue counts its rows, and the board row nothing measures is said plainly", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const needs = page.locator("[data-widget='needs.you']");
  await expect(needs.locator("[data-needs='tasks.open_shortages'] .needs-count")).toHaveText("2");
  await expect(needs.locator("[data-needs='tasks.stale_picks'] .needs-count")).toHaveText("1");
  await expect(needs.locator("[data-needs='purchasing.receiving_differences'] .needs-count")).toHaveText("1");
  await expect(needs.locator(".needs-total")).toHaveText("4 waiting · Urgent");
  await expect(needs.locator("[data-unsourced='weights.pending'] .what")).toHaveText("Weights pending");
  await expect(needs.locator("[data-unsourced='weights.pending'] .words")).toContainText("No source is wired");
  await expect(needs.locator("[data-unsourced='weights.pending'] .needs-count")).toHaveCount(0);
});

test("the board and the bars render the server's points, and the tile nothing measures is said plainly", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const board = page.locator("[data-widget='picks.board']");
  await expect(board.locator(".chip")).toHaveCount(4);
  await expect(board.locator(".chip").first()).toContainText("Waiting");
  await expect(board.locator(".chip").first()).toContainText("8");
  const bars = page.locator("[data-widget='deliveries.branch']");
  await expect(bars.locator(".bar-row")).toHaveCount(2);
  await expect(bars.locator(".bar-row .label").first()).toHaveText("Trenton");
  await expect(bars.locator(".bar-row .value").first()).toHaveText("10");
  await expect(page.locator(".who-may").filter({ hasText: "Unread messages" })).toContainText("No measure is wired");
});

test("an unmeasured card says which module is not connected, never a number", async ({ request, page }) => {
  await useScenario(request, "unwired-one");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const claims = page.locator("[data-widget='warranty.open']");
  await expect(claims.locator(".figure")).toHaveText("-");
  await expect(claims.locator(".card-sub")).toContainText("Not connected (warranty)");
});

test("the sales manager's landing comes from the sales profile: the same code, different rows", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await expect(page.locator("[data-widget]")).toHaveCount(2);
  await expect(page.locator("[data-widget='sales.today'] .figure")).toHaveText("$1,234.56");
  await expect(page.locator("[data-widget='needs.you'] [data-needs='approvals.waiting'] .needs-count")).toHaveText("1");
});

test("the theme control flips the document between light and brand", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Brand theme" })).toBeVisible();
  // The header's theme input drives the document root's attribute: light keeps
  // data-theme=light, brand (the dark half) drops it back to the sheet's dark default.
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Brand theme" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", "light");
  await expect(page.getByRole("button", { name: "Light theme" })).toBeVisible();
});

test("the needs link for shortages opens this app's own view, named with the core's words", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.locator("[data-widget='hero.deliveries']")).toBeVisible();
});

test("the needs link for receiving differences opens Receiving, not Yard Pick", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("[data-needs='purchasing.receiving_differences'] button", { hasText: "Settle" }).click();
  await expect(page.getByRole("heading", { name: "Receiving · short" })).toBeVisible();
});

test("a needs row whose surface is not on this box carries no link and says so", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const stale = page.locator("[data-needs='tasks.stale_picks']");
  await expect(stale.locator("button")).toHaveCount(0);
  await expect(stale.locator(".view-words").last()).toHaveText("not on this box yet");
});

test("the landing never scrolls sideways at 390 px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-widget='hero.deliveries']")).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
