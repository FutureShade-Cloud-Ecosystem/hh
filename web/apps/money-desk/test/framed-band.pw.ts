// PR 503 round 1 (P2-3): the band leg. Standalone, the Money Desk wears the
// ONE band: the dealer lockup, the centred "Money Desk > Approvals and ledger"
// selector, the DEMO and Synced chips (one DEMO marker, the board's outlined
// chip), the account chip with the signed-in persona and the menu carrying the
// theme control and Sign out. No app-local header, no amber Demo strip.
// Charcoal comes from the desk's own theme control in the account menu (P2-2).
// Screenshots land in G1_SHOTS_DIR when it is set (the round's evidence).
import { expect, test, type Page } from "@playwright/test";

const SHOTS = process.env.G1_SHOTS_DIR ?? "";

function controllerHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const id = "de110000-0000-4000-9000-000000000008";
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "financial-controller",
    roleLabel: "Financial Controller",
    user: { userId: id, email: "financial.controller@hh-demo.invalid", name: "Financial controller (demo)" },
    roleCode: "financial_controller",
    roleName: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

async function boot(page: Page, width: number, height: number): Promise<void> {
  await page.setViewportSize({ width, height });
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(band.locator("button.selector")).toContainText("Money Desk > Approvals and ledger");
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
    await expect(page.getByText("Everything on this box is invented demo data")).toHaveCount(0);
    await expect(page.locator("hos-app-header")).toHaveCount(0);
    // The account chip names the signed-in persona, and the menu carries the role.
    await expect(band.locator("button.account")).toContainText("Financial controller");
    await expect(band.locator("button.account")).not.toContainText("(demo)");
    await band.locator("button.account").click();
    await expect(page.getByTestId("header-word")).toHaveText("Financial Controller");
    await shot(page, `money-desk-${width}-light.png`);
    // Charcoal, from the desk's own theme control in the account menu.
    const theme = page.getByTestId("theme-control");
    await expect(theme).toBeVisible();
    await theme.click();
    await expect(theme).toContainText("Theme: Brand");
    await expect
      .poll(() => page.evaluate(() => document.documentElement.getAttribute("data-theme")))
      .toBe(null);
    await page.keyboard.press("Escape");
    await shot(page, `money-desk-${width}-dark.png`);
  });
}
