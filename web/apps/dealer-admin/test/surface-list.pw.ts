// UX-104's Playwright legs: on a phone the section's own content comes
// first, and the 15 row settings list folds behind the Settings control.
// The control opens the list by keyboard as by touch and closes it again,
// choosing a section folds it, and at the desk width the layout stands as
// it always did.
import { test, expect } from "@playwright/test";

test("on a phone the section comes first and the settings list folds behind a control", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();

  // The fold is the landed state: the control names the list it folds and
  // announces that it sits closed, and the list itself is out of the way.
  const toggle = page.locator('[data-action="toggle-surface-list"]');
  await expect(toggle).toBeVisible();
  await expect(toggle).toHaveText("Settings");
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(toggle).toHaveAttribute("aria-controls", "surface-list");
  const hand = await toggle.boundingBox();
  expect(hand).not.toBeNull();
  expect(hand!.height).toBeGreaterThanOrEqual(44);
  await expect(page.locator(".surface-list")).toBeHidden();

  // The section's own heading sits inside the first viewport, under the
  // control, not below a fifteen row list.
  const viewportHeight = page.viewportSize()!.height;
  const heading = page.locator(".surface-work h1").first();
  await expect(heading).toHaveText("Staff");
  const head = await heading.boundingBox();
  expect(head).not.toBeNull();
  expect(head!.y).toBeGreaterThanOrEqual(0);
  expect(head!.y + head!.height).toBeLessThanOrEqual(viewportHeight);

  // The control opens the list by keyboard as by touch, and closes it again.
  await toggle.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".surface-list")).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await toggle.click();
  await expect(page.locator(".surface-list")).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");

  // Opened once more, choosing a section folds the list and the chosen
  // section's own heading shows in the first viewport.
  await toggle.click();
  await expect(page.locator(".surface-list")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator(".surface-list")).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  const pricingHead = page.locator(".surface-work h1").first();
  await expect(pricingHead).toHaveText("Pricing");
  const pricingBox = await pricingHead.boundingBox();
  expect(pricingBox).not.toBeNull();
  expect(pricingBox!.y + pricingBox!.height).toBeLessThanOrEqual(viewportHeight);
});

test("at the desk width the list stands beside the work and the control is gone", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await expect(page.locator('[data-action="toggle-surface-list"]')).toBeHidden();
  await expect(page.locator(".surface-list")).toBeVisible();
  const list = await page.locator(".surface-list").boundingBox();
  const work = await page.locator(".surface-work").boundingBox();
  expect(list).not.toBeNull();
  expect(work).not.toBeNull();
  expect(work!.x).toBeGreaterThanOrEqual(list!.x + list!.width);
});
