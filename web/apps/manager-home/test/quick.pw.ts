// The quick actions, sub item 1: the landing's bar from the caller's resolved
// registry rows. The core decides who sees an action (the yard shape does not
// sell, so the yard manager's landing draws no bar); the bar carries the
// core's own words and mark; a click navigates to the destination the core
// names and never acts by itself.
//
// Sub item 2: the admin's config view. The gate is the server's, so a demo
// manager opening it reads the core's own one word refusal verbatim; under
// the stub's quick-admin scenario the view a real admin gets is walked end to
// end, save included.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, signInByCard, useScenario } from "./helpers.js";

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

test("the landing's view links carry the config view", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const links = page.locator(".view-link");
  await expect(links).toHaveCount(5);
  await expect(links.filter({ hasText: "Quick actions" })).toBeVisible();
});

test("the config view names who may configure when the core's one word wall answers", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Quick actions" }).click();
  const refusal = page.locator("[data-test='quick-refusal']");
  await expect(refusal).toHaveText(
    "You may not change the quick actions. Registry configuration belongs to the tier 1 roles: the general manager, the financial controller, and the dealer administrator.",
  );
  // No row, no try-again words dressed up as a wall.
  await expect(page.locator("[data-quick]")).toHaveCount(0);
});

test("the admin's config view shows every registered action with its resolved switch and matrix", async ({ request, page }) => {
  await useScenario(request, "quick-admin");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Quick actions" }).click();
  const row = page.locator("[data-quick='sale.new']");
  await expect(row.locator(".qa-name")).toHaveText("+ Sale");
  await expect(row.locator(".qa-id")).toHaveText("sale.new");
  await expect(row.locator(".qa-lead")).toHaveText("Jumps to Point of Sale with a fresh ticket.");
  await expect(row.locator(".qa-switch")).toHaveAttribute("aria-pressed", "true");
  // The core's matrix, resolved for every shape: no blank cell, the shipped
  // default named rather than implying somebody chose it.
  const shapes = row.locator(".qa-shape");
  await expect(shapes).toHaveCount(13);
  await expect(shapes.first()).toHaveText("Counter");
  await expect(shapes.filter({ hasText: "Manager" }).first()).toHaveAttribute("aria-pressed", "true");
  await expect(shapes.filter({ hasText: "Yard manager" })).toHaveAttribute("aria-pressed", "false");
  await expect(shapes.filter({ hasText: "Driver" })).toHaveAttribute("aria-pressed", "false");
  await expect(row.locator(".qa-default")).toHaveText("shipped default");
});

test("a saved edit lands on the server's answer, names it, and drops the default note", async ({ request, page }) => {
  await useScenario(request, "quick-admin");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Quick actions" }).click();
  const row = page.locator("[data-quick='sale.new']");
  // Turn the admin shape off and save: the draft is local until Save.
  await row.locator(".qa-shape", { hasText: "Admin" }).click();
  await expect(row.locator(".qa-shape", { hasText: "Admin" })).toHaveAttribute("aria-pressed", "false");
  await row.locator("[data-action='quick-save']").click();
  const saved = row.locator(".qa-saved");
  await expect(saved).toHaveText("+ Sale saved. Everyone picks it up on their next page load.");
  await expect(row.locator(".qa-default")).toHaveCount(0);
  await expect(row.locator(".qa-switch")).toHaveAttribute("aria-pressed", "true");
});
