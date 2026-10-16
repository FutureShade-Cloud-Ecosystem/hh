// The tenant-driven tab title: the stub serves the public dealer config and
// the page composes "<dealer> · Sign in" (the card's word, not the internal
// page name).
import { expect, test } from "@playwright/test";

test("the tab title carries the dealer name from the tenant config", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle("Gable Landscape Supply · Sign in");
});
