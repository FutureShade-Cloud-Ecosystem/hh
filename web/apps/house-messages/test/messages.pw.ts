// The app's first browser leg: boots the app in demo mode against the stub, taps a role card and
// verifies the signed-in chrome renders. The messaging legs grow from here.
import { test, expect } from "@playwright/test";

test("demo mint and signed-in render", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  const cards = page.locator("[data-flow]");
  await expect(cards.first()).toBeVisible();
  await cards.first().click();
  await expect(page.locator("hos-app-header[signed-in]")).toBeVisible();
  await expect(page.locator("[data-flow]")).toHaveCount(0);
});
