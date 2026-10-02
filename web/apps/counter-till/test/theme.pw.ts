// The theme choice in the account area: the system's light setting is honoured
// from the first paint (no choice made yet is Auto), and the Theme control
// cycles Auto, Light and Dark on the document root. The dark sheet is the
// tokens' own default, so Dark is the absence of the attribute.
import { expect, test } from "@playwright/test";

test.describe("with the system on light", () => {
  test.use({ colorScheme: "light" });

  test("Auto follows the system, and the control cycles Light, Dark, Auto", async ({ page }) => {
    await page.goto("/");
    const root = page.locator("html");
    const control = page.getByTestId("theme-control");
    await expect(control).toContainText("Theme: Auto");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await expect(control).toContainText("Theme: Light");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await expect(control).toContainText("Theme: Dark");
    await expect(root).not.toHaveAttribute("data-theme", /.*/);
    await control.click();
    await expect(control).toContainText("Theme: Auto");
    await expect(root).toHaveAttribute("data-theme", "light");
  });
});

test.describe("with the system on dark", () => {
  test.use({ colorScheme: "dark" });

  test("Auto is the dark sheet, Light pins the light one, and the control is on the till too", async ({
    page,
  }) => {
    await page.goto("/");
    const root = page.locator("html");
    await expect(root).not.toHaveAttribute("data-theme", /.*/);
    await page.getByRole("button", { name: "Sales", exact: true }).click();
    await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
    const control = page.getByTestId("theme-control");
    await expect(control).toBeVisible();
    await control.click();
    await expect(root).toHaveAttribute("data-theme", "light");
  });
});
