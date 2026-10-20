// PR 503 round 1 (P2-3): the band leg. Standalone, Buying and Stock wears the
// ONE band: the dealer lockup, the centred "Buying and Stock > Purchase orders"
// selector, the DEMO and Synced chips (one DEMO marker, the board's outlined
// chip), the account chip with the signed-in persona and the menu carrying
// Theme, Feedback, Switch role and Sign out. No app-local header, no amber
// Demo strip. Charcoal comes from the app's own theme control in the account
// menu (P2-2). Screenshots land in G1_SHOTS_DIR when it is set.
import { expect, test, type Page } from "@playwright/test";
import { procurementHandoff } from "./helpers.js";

const SHOTS = process.env.G1_SHOTS_DIR ?? "";

async function boot(page: Page, width: number, height: number): Promise<void> {
  await page.setViewportSize({ width, height });
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(band.locator("button.selector")).toContainText("Buying and Stock > Purchase orders");
}

async function shot(page: Page, name: string): Promise<void> {
  if (SHOTS === "") return;
  await page.screenshot({ path: `${SHOTS}/${name}` });
}

for (const [width, height] of [
  [1440, 900],
  [1024, 768],
] as const) {
  test(`the one band at ${width}, light then charcoal from the account menu`, async ({ page }) => {
    await boot(page, width, height);
    const band = page.locator("hh-shell-header");
    await expect(band).toContainText("Gable Landscape Supply");
    await expect(band.getByText("DEMO", { exact: true })).toHaveCount(1);
    await expect(band).toContainText("Synced");
    // One band: no app-local header, no amber Demo strip, no second lockup.
    await expect(page.locator("hos-app-header")).toHaveCount(0);
    await expect(page.getByText("Everything on this box is invented demo data")).toHaveCount(0);
    // The account chip names the signed-in persona, and the menu carries the role.
    await expect(band.locator("button.account")).toContainText("Procurement manager");
    await expect(band.locator("button.account")).not.toContainText("(demo)");
    await band.locator("button.account").click();
    await expect(page.getByTestId("signed-in-role")).toHaveText("Procurement manager (demo)");
    await shot(page, `buying-and-stock-${width}-light.png`);
    // Charcoal, from the app's own theme control in the account menu.
    const theme = page.getByTestId("theme-control");
    await expect(theme).toBeVisible();
    await theme.click();
    await expect(theme).toContainText("Theme: Brand");
    await expect
      .poll(() => page.evaluate(() => document.documentElement.getAttribute("data-theme")))
      .toBe(null);
    await page.keyboard.press("Escape");
    await shot(page, `buying-and-stock-${width}-dark.png`);
  });
}

test("this desk mounts no TanyAI panel, so its band carries no Ask TanyAI button", async ({ page }) => {
  await boot(page, 1440, 900);
  await expect(page.locator("hh-shell-header")).toBeVisible();
  await expect(page.locator("hh-shell-header").locator("[data-action='assist']")).toHaveCount(0);
});

// A second dealer, invented, served as the tenant config: the band names whoever the config names.
const SECOND_DEALER = { id: "coastal", dealer_name: "Coastal Stone Supply", logo_url: "", primary_color: "#3a7d56", support_email: "help@coastal.invalid", support_phone: "" };

test("the band names the dealer the tenant config serves, not a built in name", async ({ page }) => {
  await page.route("**/api/v1/tenant/config", (route) => route.fulfill({ json: SECOND_DEALER }));
  await boot(page, 1440, 900);
  const band = page.locator("hh-shell-header");
  await expect(band).toContainText("Coastal Stone Supply");
  await expect(band).not.toContainText("Gable Landscape Supply");
});
