// Round 14 item 25 (decision log row 1129): the person's theme choice is carried between the front door, the till and
// the Quote Desk in the tab's session storage (hh-demo.theme.v1), the same mirror, the same three values (auto, light,
// dark for Brand), anything else ignored, Light the demo default. The Quote Desk reads it at boot and writes it on its
// own Theme press. This suite serves a demo mode build; the default build's absence of all of it is pinned by
// test/no-default-adopt.test.mjs and test/demo-light-default.test.mjs, which read the built files.
import { expect, test, type Page } from "@playwright/test";

async function openAccountMenu(page: Page): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (!(await menu.isVisible())) await page.locator("hh-shell-header button.account").click();
}

test.use({ colorScheme: "light" });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a Brand choice in the tab opens the Quote Desk on Brand, and a press is written back", async ({ page }) => {
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
  await control.click();
  await expect(control).toContainText("Theme: Auto");
  expect(await page.evaluate(() => window.sessionStorage.getItem("hh-demo.theme.v1"))).toBe('"auto"');
  await page.reload();
  await openAccountMenu(page);
  await expect(page.getByTestId("theme-control")).toContainText("Theme: Auto");
});

test("a stored Light choice opens on Light, and a press to Brand is written as dark", async ({ page }) => {
  await page.addInitScript(() => {
    if (window.name === "themed") return;
    window.name = "themed";
    window.sessionStorage.setItem("hh-demo.theme.v1", '"light"');
  });
  await page.goto("/");
  const control = page.getByTestId("theme-control");
  await openAccountMenu(page);
  await expect(control).toContainText("Theme: Light");
  await control.click();
  await expect(control).toContainText("Theme: Brand");
  expect(await page.evaluate(() => window.sessionStorage.getItem("hh-demo.theme.v1"))).toBe('"dark"');
});

test("a stored Auto follows the system", async ({ page }) => {
  await page.addInitScript(() => window.sessionStorage.setItem("hh-demo.theme.v1", '"auto"'));
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  await openAccountMenu(page);
  await expect(page.getByTestId("theme-control")).toContainText("Theme: Auto");
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.*/);
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
});

test("a stored value that is not one of the three is ignored, and nothing stored is Light", async ({ page }) => {
  await page.addInitScript(() => window.sessionStorage.setItem("hh-demo.theme.v1", '"purple"'));
  await page.goto("/");
  await openAccountMenu(page);
  await expect(page.getByTestId("theme-control")).toContainText("Theme: Light");
  const plain = await page.context().newPage();
  await plain.goto("/");
  await openAccountMenu(plain);
  await expect(plain.getByTestId("theme-control")).toContainText("Theme: Light");
});
