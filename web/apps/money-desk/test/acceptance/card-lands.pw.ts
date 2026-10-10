// Acceptance legs: the cards land on the app. The front door's Payables and
// Receivables card signs the role in, its Money Desk tile opens /money-desk/,
// and the app reads the front door's handoff and lands on the receivables
// landing: signed in as that role, the four aging tiles drawn, no second sign
// in. The Financial Controller's card opens the same tile on the same path and
// lands on the ledger ("Approvals and ledger"), with the receivables desk one
// tap away; the Sales and Sales manager cards offer no Money Desk tile. Both apps are the real builds on one origin (serve-both.mjs); the mint is
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

test("the front door's Financial Controller card opens the same Money Desk tile and lands on the ledger", async ({ page }) => {
  await page.goto("/");
  const card = page.getByRole("button", { name: "Financial Controller", exact: true });
  await expect(card).toBeEnabled();
  await expect(card).toContainText("Money Desk");
  await card.click();
  await expect(page.getByTestId("home-title")).toHaveText("Financial Controller");

  const tile = page.getByRole("button", { name: "Money Desk", exact: true });
  await expect(tile).toBeVisible();
  await tile.click();

  await expect(page).toHaveURL(/\/money-desk\/$/);
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.getByRole("button", { name: "Approvals and ledger" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Receivables", exact: true })).toBeVisible();
  await expect(page.locator("[data-card]")).toHaveCount(0);
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
});

for (const label of ["Sales", "Sales manager"]) {
  test(`the front door's ${label} card offers no Money Desk tile`, async ({ page }) => {
    await page.goto("/");
    await page.getByRole("button", { name: label, exact: true }).click();
    await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Money Desk", exact: true })).toHaveCount(0);
  });
}
