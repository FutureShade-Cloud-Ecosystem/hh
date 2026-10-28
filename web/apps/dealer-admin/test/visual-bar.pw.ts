// Round 5 (VISUAL-PR608-argon): the pricing pills and tabs reach the 44 px
// phone touch bar at 390 wide; they rendered at 32 px.
//
// UX-106: the pricing navigation is ONE control on a phone: the whole list
// folds behind a select, so the select is what has to reach the touch bar.
import { test, expect } from "@playwright/test";

test("the pricing navigation at 390 is one select at the 44 px touch bar", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  // UX-104: on a phone the list waits folded behind the Settings control,
  // so the journey opens the control first and then chooses Pricing.
  await page.locator('[data-action="toggle-surface-list"]').click();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  const select = page.locator("[data-test=pr-tab-select]");
  await expect(select).toBeVisible();
  const box = await select.boundingBox();
  expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
  // The strip of pills is the desk's control: on a phone it is not on the
  // glass at all, so it cannot push the section's own content down.
  await expect(page.locator(".pr-tabs")).toBeHidden();
});
