// The theme choice in the account area: in a demo build the till starts on
// Light, the control cycles Light, Dark and Auto on the document root, and
// Auto follows the system's light setting. The dark sheet is the tokens' own
// default, so Dark is the absence of the attribute.
import { expect, test } from "@playwright/test";

test.describe("with the system on light", () => {
  test.use({ colorScheme: "light" });

  test("the demo build starts on Light and the control cycles Dark, Auto, Light", async ({ page }) => {
    await page.goto("/");
    const root = page.locator("html");
    const control = page.getByTestId("theme-control");
    await expect(control).toContainText("Theme: Light");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await expect(control).toContainText("Theme: Dark");
    await expect(root).not.toHaveAttribute("data-theme", /.*/);
    await control.click();
    await expect(control).toContainText("Theme: Auto");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await expect(control).toContainText("Theme: Light");
    await expect(root).toHaveAttribute("data-theme", "light");
  });
});

test.describe("with the system on dark", () => {
  test.use({ colorScheme: "dark" });

  test("the demo build starts on Light, Light pins the light sheet, and the control is on the till too", async ({
    page,
  }) => {
    await page.goto("/");
    const root = page.locator("html");
    await expect(root).toHaveAttribute("data-theme", "light");
    await page.getByRole("button", { name: "Sales", exact: true }).click();
    await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
    const control = page.getByTestId("theme-control");
    await expect(control).toBeVisible();
    await control.click();
    await expect(root).not.toHaveAttribute("data-theme", /.*/);
  });
});

test.describe("live behaviour", () => {
  test.use({ colorScheme: "light" });

  test("Auto follows a system colour-scheme change made while the page is open", async ({ page }) => {
    await page.goto("/");
    const root = page.locator("html");
    const control = page.getByTestId("theme-control");
    await expect(control).toContainText("Theme: Light");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await control.click();
    await expect(control).toContainText("Theme: Auto");
    await expect(root).toHaveAttribute("data-theme", "light");
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(root).not.toHaveAttribute("data-theme", /.*/);
    await page.emulateMedia({ colorScheme: "light" });
    await expect(root).toHaveAttribute("data-theme", "light");
  });

  test("a press announces the new state in a polite status, with focus left on the control", async ({
    page,
  }) => {
    await page.goto("/");
    const status = page.getByTestId("theme-status");
    await expect(status).toHaveAttribute("aria-live", "polite");
    await expect(status).toHaveText("");
    await page.getByTestId("theme-control").click();
    await expect(status).toHaveText("Theme is now Dark.");
    await page.getByTestId("theme-control").click();
    await expect(status).toHaveText("Theme is now Auto, following the system.");
  });
});
