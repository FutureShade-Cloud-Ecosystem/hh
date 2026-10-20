// The signed-in chrome (visual review round 1, P2-1; reshaped by the ONE
// band adoption, PR 503 round 1): the chrome IS the shell chrome's
// hh-shell-header in band variant. The desk keeps the one band: the dealer
// lockup, the centred selector, the DEMO and Synced chips, and the account
// chip's menu carries Theme, Feedback, Switch role and Sign out. These
// check the band's shape, not its pixels.
import { expect, test, type Page } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openAccountMenu(page: Page): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (!(await menu.isVisible())) await page.locator("hh-shell-header button.account").click();
}

for (const width of [1440, 820]) {
  test(`the band carries the lockup, the role and Sign out at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    const band = page.locator("hh-shell-header");
    await expect(band).toBeVisible();
    await expect(band).toContainText("Gable Landscape Supply");
    await expect(band.locator("button.selector")).toContainText("Quote Desk > Quotes");
    await expect(band.getByText("DEMO", { exact: true })).toHaveCount(1);
    await expect(band).toContainText("Synced");
    const bandBox = (await band.boundingBox())!;
    expect(bandBox.x).toBe(0);
    expect(bandBox.width).toBe(width);
    await openAccountMenu(page);
    await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
    const signOut = page.getByRole("button", { name: "Sign out", exact: true });
    await expect(signOut).toHaveClass(/btn-secondary/);
    const signBox = (await signOut.boundingBox())!;
    expect(signBox.height).toBeLessThanOrEqual(48);
    expect(signBox.x + signBox.width).toBeLessThanOrEqual(width - 12);
    const toggle = page.getByTestId("theme-control");
    await expect(toggle).toBeVisible();
    const toggleBox = (await toggle.boundingBox())!;
    expect(toggleBox.x + toggleBox.width).toBeLessThanOrEqual(width - 12);
    expect(toggleBox.y).toBeGreaterThanOrEqual(bandBox.y);
  });
}

// The theme control (visual review round 1, P2-5): the ERP's light look was
// unreachable. The band's account menu carries the till's cycle (Auto, Light,
// Brand); the brand sheet (the dark half) is the tokens' default and light is the opt-in
// data-theme attribute. This suite serves a demo mode build (playwright.config.ts
// runs vite --mode demo), and a demo build starts on Light (decision log row
// 978), so the cycle here runs Light, Brand, Auto. That a default build starts
// on Auto is proved on the shipped bundle by test/demo-light-default.test.mjs,
// which builds both ways; the two together cover both starting states.
test("the theme control starts on Light in the demo build and cycles Light, Brand, Auto", async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: "dark" });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await openAccountMenu(page);
  const toggle = page.getByTestId("theme-control");
  const root = page.locator("html");
  // The system is dark, so a light root can only be the demo build's own start.
  await expect(toggle).toHaveText("Theme: Light");
  await expect(root).toHaveAttribute("data-theme", "light");
  // The desk itself re-themes: the page ground is light.
  const ground = await page.locator("qd-app-root").evaluate((el) => getComputedStyle(el).backgroundColor);
  const [red, green, blue] = ground.match(/\d+/g)!.map(Number);
  expect(red + green + blue).toBeGreaterThan(600);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Brand");
  await expect(root).not.toHaveAttribute("data-theme", /.+/);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Auto");
  await expect(root).not.toHaveAttribute("data-theme", /.+/);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Light");
  await context.close();
});

// Reached through the cycle, because the demo build starts pinned on Light and
// a light root at load would pass without Auto following anything.
test("Auto follows the system's light setting", async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: "dark" });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  const toggle = page.getByTestId("theme-control");
  await openAccountMenu(page);
  await toggle.click();
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Auto");
  const root = page.locator("html");
  await expect(root).not.toHaveAttribute("data-theme", /.+/);
  await page.emulateMedia({ colorScheme: "light" });
  await expect(root).toHaveAttribute("data-theme", "light");
  await context.close();
});

// The face is Outfit itself, self-hosted. document.fonts.load answers with the
// faces it actually matched, so an absent @font-face fails here as an empty
// answer rather than passing on check()'s vacuous true.
test("the desk's UI face is Outfit, self-hosted, not a system fallback", async ({ page }) => {
  await signIn(page);
  const faces = await page.evaluate(async () => {
    const out: Record<string, boolean> = {};
    for (const weight of ["400", "500", "600", "700"]) {
      const matched = await document.fonts.load(`${weight} 16px Outfit`);
      out[weight] = matched.length > 0 && matched.every((face) => face.status === "loaded");
    }
    await document.fonts.ready;
    return out;
  });
  expect(faces).toEqual({ 400: true, 500: true, 600: true, 700: true });
});
