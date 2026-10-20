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

const WITH_ASSIST = true;
const APP = "money-desk";

// PR 503 round (P2-1): at 390 the dealer lockup, the DEMO marker and the other run controls never overlap.
async function boxesOf(page: Page, selectors: string[]): Promise<Array<{ name: string; x: number; y: number; w: number; h: number }>> {
  const out: Array<{ name: string; x: number; y: number; w: number; h: number }> = [];
  for (const selector of selectors) {
    const target = page.locator("hh-shell-header").locator(selector).first();
    if ((await target.count()) === 0) continue;
    if (!(await target.isVisible())) continue;
    const box = (await target.boundingBox())!;
    out.push({ name: selector, x: box.x, y: box.y, w: box.width, h: box.height });
  }
  return out;
}

function overlaps(a: { x: number; y: number; w: number; h: number }, b: { x: number; y: number; w: number; h: number }): boolean {
  return a.x < b.x + b.w - 1 && b.x < a.x + a.w - 1 && a.y < b.y + b.h - 1 && b.y < a.y + a.h - 1;
}

for (const mode of ["light", "charcoal"] as const) {
  test(`at 390 the lockup, DEMO${WITH_ASSIST ? " and the assist icon" : ""} have no overlapping boxes (${mode})`, async ({ page }) => {
    await boot(page, 390, 844);
    if (mode === "charcoal") {
      await page.locator("hh-shell-header button.account").click();
      const theme = page.getByTestId("theme-control");
      if (!((await theme.textContent()) ?? "").includes("Brand")) await theme.click();
      await expect(theme).toContainText("Brand");
      await page.keyboard.press("Escape");
    }
    await shot(page, `${APP}-390-${mode}.png`);
    const boxes = await boxesOf(page, [".lockup", ".demo", ".chip", "button[data-action='assist']", "button[data-action='notifications']", "button.account"]);
    const lockup = boxes.find((b) => b.name === ".lockup")!;
    expect(lockup.w).toBeGreaterThan(40);
    for (let i = 0; i < boxes.length; i += 1) {
      for (let j = i + 1; j < boxes.length; j += 1) {
        expect(overlaps(boxes[i], boxes[j]), `${boxes[i].name} and ${boxes[j].name} overlap at 390 (${mode})`).toBe(false);
      }
    }
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(scroll).toBeLessThanOrEqual(0);
  });
}

test("money-desk mounts a panel, so its band carries one Ask TanyAI button that opens it", async ({ page }) => {
  await boot(page, 1440, 900);
  const button = page.locator("hh-shell-header").locator("button[data-action='assist']");
  await expect(button).toHaveCount(1);
  await button.click();
  await expect(page.getByRole("dialog", { name: "TanyAI" })).toBeVisible();
});
