// Round 13: the front door has the till's Theme control (Auto, Light, Brand; Light the demo default),
// Auto follows the operating system's light setting (live), and the choice is carried to the till and
// the Quote Desk through the tab's session storage (the same storage the demo handoff rides).
import { expect, test, type Page } from "@playwright/test";

const THEME_KEY = "hh-demo.theme.v1";

const attr = (page: Page) => page.evaluate(() => document.documentElement.getAttribute("data-theme"));

test.beforeEach(async ({ page }) => {
  const reset = await page.request.post("/_stub/reset");
  expect(reset.ok()).toBe(true);
});

for (const where of ["picker", "home"] as const) {
  test(`the ${where} carries a Theme control that cycles Light, Brand, Auto`, async ({ page }) => {
    await page.goto("/");
    if (where === "home") {
      await page.getByRole("button", { name: "Sales", exact: true }).click();
      await expect(page.getByTestId("header-word")).toHaveText("Sales");
    }
    const control = page.getByTestId("theme-control");
    await expect(control).toBeVisible();
    // Light is the demo default.
    await expect(control).toHaveText("Theme: Light");
    expect(await attr(page)).toBe("light");
    await control.click();
    await expect(control).toHaveText("Theme: Brand");
    expect(await attr(page)).toBeNull();
    await control.click();
    await expect(control).toHaveText("Theme: Auto");
    await control.click();
    await expect(control).toHaveText("Theme: Light");
    expect(await attr(page)).toBe("light");
  });
}

test("Auto follows the system: dark preference gives Brand, light gives Light, live", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  // The demo default is Light even under a dark preference: the box's first screen is light.
  expect(await attr(page)).toBe("light");
  const control = page.getByTestId("theme-control");
  await control.click(); // Brand
  await control.click(); // Auto
  await expect(control).toHaveText("Theme: Auto");
  expect(await attr(page)).toBeNull();
  await page.emulateMedia({ colorScheme: "light" });
  await expect.poll(() => attr(page)).toBe("light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect.poll(() => attr(page)).toBeNull();
});

test("the choice survives a reload and a sign in, and rests in the shared session key", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("theme-control").click(); // Brand
  await expect(page.getByTestId("theme-control")).toHaveText("Theme: Brand");
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), THEME_KEY)).toBe('"dark"');
  await page.reload();
  await expect(page.getByTestId("theme-control")).toHaveText("Theme: Brand");
  expect(await attr(page)).toBeNull();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
  await expect(page.getByTestId("theme-control")).toHaveText("Theme: Brand");
  expect(await attr(page)).toBeNull();
  // Sign out keeps the person's theme: it is not part of the session.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByTestId("theme-control")).toHaveText("Theme: Brand");
});

test("a stored choice that is not one of the three is ignored", async ({ page }) => {
  await page.addInitScript((key) => window.sessionStorage.setItem(key, '"purple"'), THEME_KEY);
  await page.goto("/");
  await expect(page.getByTestId("theme-control")).toHaveText("Theme: Light");
});
