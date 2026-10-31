// UX-125: the pricing log's Change column words the floor check's shorthand,
// and the roles grid's Manager line is one short line.
// Blank check (item 386): set UX125_BLANK_MODE to "screen" or "core" to verify
// the leg fails on an empty screen and on an empty core.
import { expect, test, type Page } from "@playwright/test";

const UX125_BLANK_MODE = "" as "" | "screen" | "core";
const MANAGER_ROW = "0e110000-0000-4000-9000-0000000000a3";
const SALES_ROW = "0e110000-0000-4000-9000-0000000000a4";

async function signIn(page: Page): Promise<void> {
  await page.request.post("/_stub/reset");
  if (UX125_BLANK_MODE === "core") await page.request.post("/_stub/scenario", { data: { name: "empty-pricing" } });
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count], [data-test=staff-empty]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function openSurface(page: Page, surface: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    const list = page.locator(".surface-list");
    if (!(await list.isVisible())) await page.locator('[data-action="toggle-surface-list"]').click();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await page.waitForLoadState("networkidle");
}

test.afterEach(async ({ page }) => {
  if (UX125_BLANK_MODE !== "") await page.request.post("/_stub/reset");
});

test("UX-125: a margin enforcement row's Change reads in a dealer's words", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openSurface(page, "pricing");
  await page.locator(".pr-tab").filter({ hasText: "Pricing audit" }).first().click();
  await expect(page.locator('[data-test="pr-audit-count"], [data-test="pr-audit-empty"]').first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  if (UX125_BLANK_MODE === "screen") await page.setContent("");

  const bypass = page.locator(`tr[data-audit="${MANAGER_ROW}"] td[data-label="Change"]`);
  await expect(bypass).toHaveText("Was: rule price $7.45, 12% off. Now: net $6.55, margin 11.89%, floor 20.00%.", { timeout: 5_000 });
  const update = page.locator(`tr[data-audit="${SALES_ROW}"] td[data-label="Change"]`);
  await expect(update).toHaveText("Was: rule price $7.45, 0% off. Now: net $7.45, margin 11.89%, floor 20.00%.");
  await expect(page.locator(".surface-work")).not.toContainText("net 655");
});

test("UX-125: the Entity cell never flashes a wrong word while the lookups settle", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openSurface(page, "pricing");
  await page.locator(".pr-tab").filter({ hasText: "Pricing audit" }).first().click();
  const cell = page.locator(`tr[data-audit="${SALES_ROW}"] td[data-label="Entity"]`);
  await expect(cell).toContainText("Brussels Dimensional", { timeout: 10_000 });
  await expect(page.locator(".surface-work")).not.toContainText("A product");
});

test("UX-126: the Entity cell holds while the product lookups are in flight, then names the product", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let held = 0;
  await page.route(/\/api\/v1\/products\/[^/?]+(\?.*)?$/, async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    held += 1;
    await gate;
    await route.fallback();
  });
  await signIn(page);
  await openSurface(page, "pricing");
  await page.locator(".pr-tab").filter({ hasText: "Pricing audit" }).first().click();
  await expect(page.locator('[data-test="pr-audit-count"], [data-test="pr-audit-empty"]').first()).toBeVisible({ timeout: 30_000 });
  if (UX125_BLANK_MODE === "screen") await page.setContent("");
  const cell = page.locator(`tr[data-audit="${SALES_ROW}"] td[data-label="Entity"]`);
  await expect(cell).toBeVisible();
  await expect.poll(() => held, { timeout: 10_000 }).toBeGreaterThan(0);
  // The lookup is held: the cell must read the type word alone, never the wrong placeholder.
  await expect(cell).not.toContainText("A product");
  await expect(cell).toHaveText("Margin enforcement");
  await page.waitForTimeout(500);
  await expect(cell).not.toContainText("A product");
  release();
  await expect(cell).toContainText("Brussels Dimensional", { timeout: 10_000 });
  await expect(page.locator(".surface-work")).not.toContainText("A product");
});
