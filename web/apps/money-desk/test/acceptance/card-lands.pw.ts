// Acceptance leg: the card lands on the app. The front door's Payables and
// Receivables card signs the role in, its Money Desk tile opens /money-desk/,
// and the app reads the front door's handoff and lands on the receivables
// landing: signed in as that role, the four aging tiles drawn, no second sign
// in. Both apps are the real builds on one origin (serve-both.mjs); the mint is
// the Money Desk stub's, in the core's shape.
import { test, expect } from "@playwright/test";

test("the front door's Payables and Receivables card opens the Money Desk on the receivables landing", async ({ page }) => {
  await page.goto("/");
  const card = page.getByRole("button", { name: "Payables and Receivables", exact: true });
  await expect(card).toBeEnabled();
  await expect(card).toContainText("Money Desk");
  await card.click();
  await expect(page.getByTestId("home-title")).toHaveText("Payables and Receivables");

  const tile = page.getByRole("button", { name: "Money Desk", exact: true });
  await expect(tile).toBeVisible();
  await tile.click();

  await expect(page).toHaveURL(/\/money-desk\/$/);
  await expect(page.getByRole("heading", { name: "Receivables", exact: true })).toBeVisible();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await expect(page.locator("[data-flow]")).toHaveCount(0);
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("[data-register-row]").first()).toBeVisible();
});
