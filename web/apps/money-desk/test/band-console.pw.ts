// UX-121b: the band on a dealer's own box. A page served a console /config.json names the dealer from
// dealer.name and wears no DEMO chip; a page served no console config stays DEMO (item 370).
import { expect, test } from "@playwright/test";

const CONSOLE_CONFIG = {
  mode: "console",
  org: "acme-stone",
  consoleEndpoint: "https://console.invalid/v1",
  consoleProjectId: "acme-project",
  coreBaseUrl: "",
  personas: false,
  personasOpen: false,
  dealer: { name: "Acme Stone" },
};

test("a console box names its dealer in the band and wears no DEMO chip", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(band).toContainText("Acme Stone");
  await expect(band.getByText("DEMO", { exact: true })).toHaveCount(0);
});

test("a page served no console config stays DEMO", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(band.getByText("DEMO", { exact: true })).toHaveCount(1);
  await expect(band).not.toContainText("Acme Stone");
});
