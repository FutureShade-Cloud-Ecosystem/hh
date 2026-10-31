// UX-127: the words "Demo data only." belong to a demo page; a dealer's own box does not carry them.
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

test("a console box's page copy drops the demo-data words", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Demo data only");
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\.$/);
  // UX-127 read P2-1: the header's demo banner belongs to a demo page too.
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("hos-app-header")).not.toHaveAttribute("demo", "");
  await expect(page.locator("hos-app-header .demo-banner")).toHaveCount(0);
});

test("a page served no console config still says demo data only", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\. Demo data only\.$/);
  await expect(page.locator("hos-app-header")).toHaveAttribute("demo", "");
  await expect(page.locator("hos-app-header .demo-banner")).toHaveCount(1);
});
