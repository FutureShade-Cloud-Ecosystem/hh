// PR 503 round 1 (P2-3): the band leg. Standalone, the till wears the ONE
// band: the dealer lockup, the centred "Counter Till > New sale" selector,
// the DEMO and Synced chips (one DEMO marker, the board's outlined chip),
// the account chip with the signed-in persona and the menu carrying Theme,
// Feedback and Sign out. No app-local header, no amber Demo strip. Charcoal
// comes from the till's own theme control in the account menu (P2-2).
// Screenshots land in G1_SHOTS_DIR when it is set (the round's evidence).
import { expect, test, type Page } from "@playwright/test";

const HANDOFF_KEY = "hh-demo.front-door.session.v1";
const SHOTS = process.env.G1_SHOTS_DIR ?? "";

function handoffRecord(): Record<string, unknown> {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${segment({ alg: "ES256", typ: "JWT" })}.${segment({
    iss: "hh-demo-stub",
    sub: "demo-account-sales",
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  return {
    token,
    tokenExpiresAtMs: (issuedAtSeconds + 60 * 60) * 1000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  };
}

async function boot(page: Page, width: number, height: number): Promise<void> {
  await page.setViewportSize({ width, height });
  await page.addInitScript(
    ({ key, value }) => {
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(handoffRecord()) },
  );
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(page.locator("hos-pos-terminal")).toBeAttached();
  await expect(band.locator("button.selector")).toContainText("Counter Till > New sale");
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
    await expect(page.getByTestId("till-header-badge")).toHaveCount(0);
    // The account chip names the signed-in persona, and the menu carries the role.
    await expect(band.locator("button.account")).toContainText("Sales");
    await expect(band.locator("button.account")).not.toContainText("(demo)");
    await band.locator("button.account").click();
    await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
    await shot(page, `counter-till-${width}-light.png`);
    // Charcoal, from the till's own theme control in the account menu (Auto, Light, Brand).
    const theme = page.getByTestId("theme-control");
    await expect(theme).toBeVisible();
    if ((await theme.textContent())?.includes("Auto")) await theme.click();
    await theme.click();
    await expect(theme).toContainText("Brand");
    await expect
      .poll(() => page.evaluate(() => document.documentElement.getAttribute("data-theme")))
      .toBe(null);
    await page.keyboard.press("Escape");
    await shot(page, `counter-till-${width}-dark.png`);
  });
}
