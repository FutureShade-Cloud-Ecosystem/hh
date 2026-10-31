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
  const band = page.locator("hh-field-frame");
  await expect(band).toBeVisible();
  await expect(band).toContainText("Acme Stone");
  await expect(band.getByText("DEMO", { exact: true })).toHaveCount(0);
});

test("a page served no console config stays DEMO", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  const band = page.locator("hh-field-frame");
  await expect(band).toBeVisible();
  await expect(band.getByText("DEMO", { exact: true })).toHaveCount(1);
  await expect(band).not.toContainText("Acme Stone");
});

// UX-127: the words "Demo data only." belong to a demo page; a dealer's own box does not carry them.
test("a console box's page copy drops the demo-data words", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Demo data only");
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\.$/);
});

test("a page served no console config still says demo data only", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\. Demo data only\.$/);
});
