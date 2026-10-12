// Yard Pick, sub item 1: the app from the template, phone and tablet first,
// light by default, the shared header saying the product's name. The legs
// prove the shell: the way in offers the Yard Team's own card, the header
// carries the product name in its compact phone shape, the page never
// scrolls sideways at 390 or 1024, and the light theme is the default with
// brand one tap away.
import { test, expect } from "@playwright/test";
import { yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test("the way in offers the Yard Team's own card, and no other role's", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toHaveText("Yard Team");
  await expect(page.getByRole("heading", { name: "Yard Pick" })).toBeVisible();
});

test("the shared header says the product's name, compact, in both halves of the app", async ({ page }) => {
  await page.goto("/");
  const header = page.locator("hos-app-header");
  await expect(header).toHaveAttribute("product-name", "Yard Pick");
  await expect(header).toHaveAttribute("compact", "");
  await page.getByRole("button", { name: "Yard Team" }).click();
  await expect(page.locator("main").first()).toBeVisible();
    await expect(page.locator("[data-branch]")).toBeVisible();
  await expect(header).toHaveAttribute("product-name", "Yard Pick");
  await expect(header).toHaveAttribute("signed-in", "");
});

test("the mint lands signed in through the front door's handoff", async ({ page }) => {
  await page.addInitScript(yardHandoff);
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "");
});

test("light is the default and brand is one tap away, and it stays put", async ({ page }) => {
  await page.addInitScript(yardHandoff);
  await page.goto("/");
  // The header applies the theme to the document root: light sets the sheet's
  // attribute, brand (the dark half) clears it.
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Brand theme" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Light theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
});

for (const size of [
  { width: 390, height: 844 },
  { width: 1024, height: 768 },
]) {
  test(`the page never scrolls sideways at ${size.width} px, signed in`, async ({ page }) => {
    await page.setViewportSize(size);
    await page.addInitScript(yardHandoff);
    await page.goto("/");
    await expect(page.locator("main").first()).toBeVisible();
    await expect(page.locator("[data-branch]")).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
}
