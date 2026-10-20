// Acceptance leg: the card lands on the app. The front door's Procurement
// manager card signs the role in, its Buying and Stock tile opens
// /buying-and-stock/, and the app reads the front door's handoff and lands on
// the desk: signed in as that role, the worklist with the K08 gap drawn, no
// second sign in. Both apps are the real builds on one origin
// (test/acceptance/serve-both.mjs); the mint is the Buying and Stock stub's,
// in the core's shape.
import { test, expect } from "@playwright/test";

test("the front door's Procurement manager card opens the Buying and Stock app on the desk", async ({ page }) => {
  await page.goto("/");
  const card = page.getByRole("button", { name: "Procurement manager", exact: true });
  await expect(card).toBeEnabled();
  await expect(card).toContainText("Buy the branch's stock and track what is incoming.");
  await expect(card).toContainText("Buying and Stock");
  await card.click();
  await expect(page.getByTestId("home-title")).toHaveText("Procurement manager");

  const tile = page.getByRole("button", { name: "Buying and Stock", exact: true });
  await expect(tile).toBeVisible();
  await tile.click();

  await expect(page).toHaveURL(/\/buying-and-stock\/$/);
  await expect(page.getByRole("heading", { name: "Buying and Stock", level: 1 })).toBeVisible();
  await expect(page.locator("[data-flow]")).toHaveCount(0);
  await expect(page.locator("hh-shell-header")).toBeVisible();
  await expect(page.locator("hh-shell-header")).toContainText("Procurement manager");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
});
