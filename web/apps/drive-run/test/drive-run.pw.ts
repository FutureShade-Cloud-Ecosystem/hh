// HH Drive, sub item 1: the shell. The app boots in demo mode against the
// stub, the driver's card is the way in, and the front door's handoff lands
// the driver signed in with no second picker. Phone first: 390 px wide.
import { test, expect } from "@playwright/test";
import { driverHandoff, resetStub } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the driver's card is the one way in and signs in as the Driver", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toHaveAttribute("data-flow", "driver");
  await cards.first().click();
  await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "");
  await expect(page.getByTestId("header-word")).toHaveText("Driver");
});

test("the front door's handoff lands signed in on the run sheet with no picker", async ({ page }) => {
  await page.addInitScript(driverHandoff);
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(0);
  await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "");
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
});

test("the shell is one column that never scrolls sideways at 390 px", async ({ page }) => {
  await page.addInitScript(driverHandoff);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  const meta = await page.locator('meta[name="viewport"]').getAttribute("content");
  expect(meta).toContain("viewport-fit=cover");
});
