// The tenant-driven tab title: the stub serves the public dealer config and
// the page composes "<dealer> · Quote Desk".
import { expect, test } from "@playwright/test";
import { signIn } from "./switch-helpers.js";

test("the entry tab title carries the dealer name from the tenant config", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle("Gable Landscape Supply · Quote Desk");
});

test("the list route title carries the dealer name", async ({ page }) => {
  await signIn(page);
  await expect(page).toHaveTitle("Gable Landscape Supply · Quotes");
});
