// Round 5 (VISUAL-PR608-argon): the pricing pills and tabs reach the 44 px
// phone touch bar at 390 wide; they rendered at 32 px.
import { test, expect } from "@playwright/test";

test("the pricing pills and tabs reach the 44 px touch bar at 390", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  // UX-104: on a phone the list waits folded behind the Settings control,
  // so the journey opens the control first and then chooses Pricing.
  await page.locator('[data-action="toggle-surface-list"]').click();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator('.pr-board-pill[data-tab="payment-terms"]')).toBeVisible();
  for (const sel of [".pr-board-pill", ".pr-tab"]) {
    const control = page.locator(sel).first();
    await expect(control).toBeVisible();
    const box = await control.boundingBox();
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
  }
});
