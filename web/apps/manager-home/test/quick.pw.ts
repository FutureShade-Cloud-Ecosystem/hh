// The quick actions, sub item 1: the landing's bar from the caller's resolved
// registry rows. The core decides who sees an action (the yard shape does not
// sell, so the yard manager's landing draws no bar); the bar carries the
// core's own words and mark; a click navigates to the destination the core
// names and never acts by itself.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, signInByCard } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the yard manager's landing draws no quick action: the core resolves the yard shape none", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-quick-bar]")).toHaveCount(0);
});

test("the sales manager's landing draws the caller's action with the core's words and mark", async ({ page }) => {
  await signInByCard(page, "sales-manager");
  const bar = page.locator("[data-quick-bar]");
  await expect(bar).toBeVisible();
  const run = bar.locator("[data-action='quick-run'][data-action-id='sale.new']");
  await expect(run).toHaveCount(1);
  await expect(run.locator(".word")).toHaveText("Sale");
  // The accessible name is the core's one sentence (its tooltip and its name
  // are the same string by the registry's own rule).
  await expect(run).toHaveAttribute("aria-label", "Start a new counter sale at the till.");
  // The destination is the till at the deployment prefix, carried where the
  // click's navigation reads it.
  await expect(run).toHaveAttribute("data-href", "/till/");
  // The mark is drawn geometry (the add mark for a leading-plus label), never
  // a character the machine may have no font for.
  await expect(run.locator(".mark svg")).toHaveCount(1);
});

test("clicking the action navigates to the till the core names, and acts nowhere else", async ({ page }) => {
  await signInByCard(page, "sales-manager");
  await page.locator("[data-quick-bar] [data-action='quick-run']").click();
  await page.waitForURL((url) => url.pathname === "/till/");
  expect(page.url().endsWith("/till/")).toBe(true);
});
