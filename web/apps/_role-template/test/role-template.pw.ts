// The role app template's Playwright leg: boots the app in demo mode, taps
// the sales role card, and verifies the signed-in state renders.
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
