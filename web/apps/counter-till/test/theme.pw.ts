// The theme choice in the account area: in a demo build the till starts on
// Light, the control cycles Light, Brand and Auto on the document root, and
// Auto follows the system's light setting. The dark sheet is the tokens' own
// default, so Brand is the absence of the attribute.
import { expect, test } from "@playwright/test";
import { closeAccountMenu, openAccountMenu } from "./account-menu";

test.describe("with the system on light", () => {
  test.use({ colorScheme: "light" });

  test("the demo build starts on Light and the control cycles Brand, Auto, Light", async ({ page }) => {
    await page.goto("/");
    const root = page.locator("html");
    const control = page.getByTestId("theme-control");
    await openAccountMenu(page);
    await expect(control).toContainText("Theme: Light");
    await expect(root).toHaveAttribute("data-theme", "light");
    await control.click();
    await expect(control).toContainText("Theme: Brand");
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
    await openAccountMenu(page);
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
    await openAccountMenu(page);
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
    await openAccountMenu(page);
    await page.getByTestId("theme-control").click();
    await closeAccountMenu(page);
    await expect(status).toHaveText("Theme is now Brand.");
    await openAccountMenu(page);
    await page.getByTestId("theme-control").click();
    await closeAccountMenu(page);
    await expect(status).toHaveText("Theme is now Auto, following the system.");
  });
});

// Round 13: the person's choice is carried between the front door, the till and the Quote Desk in the
// tab's session storage (hh-demo.theme.v1, written by the front door and by each app's control).
test.describe("the choice carried from the front door", () => {
  test.use({ colorScheme: "light" });

  test("a Brand choice in the tab opens the till on Brand, and a press is written back", async ({ page }) => {
    await page.addInitScript(() => {
      if (window.name === "themed") return;
      window.name = "themed";
      window.sessionStorage.setItem("hh-demo.theme.v1", '"dark"');
    });
    await page.goto("/");
    const control = page.getByTestId("theme-control");
    await openAccountMenu(page);
    await expect(control).toContainText("Theme: Brand");
    await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.*/);
    await control.click(); // Auto
    await expect(control).toContainText("Theme: Auto");
    expect(await page.evaluate(() => window.sessionStorage.getItem("hh-demo.theme.v1"))).toBe('"auto"');
    // A reload keeps it: the till no longer starts over on Light.
    await page.reload();
    await openAccountMenu(page);
    await expect(page.getByTestId("theme-control")).toContainText("Theme: Auto");
    await closeAccountMenu(page);
  });

  test("a stored Auto follows the system, and a value that is no choice leaves Light", async ({ page }) => {
    await page.addInitScript(() => window.sessionStorage.setItem("hh-demo.theme.v1", '"auto"'));
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/");
    await openAccountMenu(page);
    await expect(page.getByTestId("theme-control")).toContainText("Theme: Auto");
    await closeAccountMenu(page);
    await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.*/);
    await page.emulateMedia({ colorScheme: "light" });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  });

  test("a stored value that is not one of the three is ignored", async ({ page }) => {
    await page.addInitScript(() => window.sessionStorage.setItem("hh-demo.theme.v1", '"purple"'));
    await page.goto("/");
    await openAccountMenu(page);
    await expect(page.getByTestId("theme-control")).toContainText("Theme: Light");
    await closeAccountMenu(page);
  });
});
