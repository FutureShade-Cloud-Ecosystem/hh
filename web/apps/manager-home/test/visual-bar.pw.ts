// Round 5 (VISUAL-PR608-argon): the visual bar's own legs, red first.
//
// - The strip tiles carry no white box in charcoal: the unscoped `.tile` rule
//   painted `#FFFFFF` around every strip card on the `#141619` canvas.
// - The landing's phone controls reach the 44 px touch bar: edit, foot, chip,
//   queue and view links rendered at 36 px at 390 wide.
import { test, expect } from "@playwright/test";
import { managerHandoff } from "./helpers.js";

test("the strip tiles carry no white box in charcoal, and the strip keeps its gap", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Brand theme" }).click();
  await page.keyboard.press("Escape");
  const tile = page.locator(".strip .tile").first();
  await expect(tile).toBeVisible();
  const bg = await tile.evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(bg).not.toBe("rgb(255, 255, 255)");

  const strip = page.locator(".strip");
  const stripBox = await strip.boundingBox();
  const after = await page.locator(".c10-landing").boundingBox();
  expect(stripBox && after ? after.y - (stripBox.y + stripBox.height) : 0).toBeGreaterThan(8);
});

test("the landing's phone controls reach the 44 px touch bar at 390", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  for (const sel of [".edit-open", ".row-chip", ".foot-link", ".queue-link", ".view-link"]) {
    const control = page.locator(sel).first();
    await expect(control).toBeVisible();
    const box = await control.boundingBox();
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
  }
});
