// Dealer Admin's Playwright leg, at the template's shape: boots the app in
// demo mode, taps the first role card, and verifies the signed-in state
// renders. The surfaces' own legs join this file as they land.
import { test, expect } from "@playwright/test";

test("demo mint and signed-in render", async ({ page }) => {
  await page.goto("/");
  // The app renders the demo entry page.
  await expect(page.locator("hos-app-header")).toBeVisible();
  // Tap the first role card.
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(2);
  await cards.first().click();
  // After mint the signed-in chrome renders.
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("main")).toContainText("Inside Sales");
});
