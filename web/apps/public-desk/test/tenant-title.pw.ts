// The tenant-driven tab title for a customer quote: the stub serves the public
// dealer config and the page composes "Your quote from <dealer>".
import { expect, test } from "@playwright/test";

test("the tab title reads 'Your quote from <dealer>'", async ({ page }) => {
  await page.goto("/?t=q-demo-25");
  await expect(page).toHaveTitle("Your quote from Gable Landscape Supply");
});
