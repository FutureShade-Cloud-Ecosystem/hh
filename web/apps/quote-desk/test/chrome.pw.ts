// The signed-in chrome (visual review round 1, P2-1): the entry page had a top
// bar and every signed-in screen dropped it. The desk now keeps one bar: the
// mark and title, the demo as a slim strip, the role label and Sign out as a
// proper secondary button. These check the bar's shape, not its pixels.
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

for (const width of [1440, 820]) {
  test(`the signed-in bar carries the mark, title, role and Sign out at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    const bar = page.getByTestId("qd-bar");
    await expect(bar).toBeVisible();
    await expect(bar.getByTestId("qd-title")).toHaveText("Quote Desk");
    await expect(bar.getByTestId("qd-mark")).toBeVisible();
    const role = bar.getByTestId("signed-in-role");
    await expect(role).toHaveText("Sales");
    const roleBox = (await role.boundingBox())!;
    // Padded off the screen edge, not flush at x=0.
    expect(roleBox.x).toBeGreaterThanOrEqual(0);
    const barBox = (await bar.boundingBox())!;
    expect(barBox.x).toBe(0);
    const signOut = bar.getByRole("button", { name: "Sign out" });
    await expect(signOut).toHaveClass(/btn-secondary/);
    const signBox = (await signOut.boundingBox())!;
    expect(signBox.height).toBeLessThanOrEqual(40);
    expect(signBox.x + signBox.width).toBeLessThanOrEqual(width - 12);
    // The demo marker is a slim strip inside the bar, not a full-bleed fill.
    const demo = bar.getByRole("note");
    await expect(demo).toBeVisible();
    const demoBox = (await demo.boundingBox())!;
    expect(demoBox.height).toBeLessThanOrEqual(36);
    expect(demoBox.width).toBeLessThan(width - 24);
    expect(demoBox.x).toBeGreaterThan(0);
    // The bar itself stays compact like the reference header.
    expect(barBox.height).toBeLessThanOrEqual(120);
  });
}

// The theme control (visual review round 1, P2-5): the ERP's light look was
// unreachable. The bar carries the till's cycle (Auto, Light, Dark); the dark
// sheet is the tokens' default and light is the opt-in data-theme attribute.
test("the theme control cycles Auto, Light, Dark on the document root", async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: "dark" });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const toggle = page.getByTestId("qd-bar").getByTestId("theme-control");
  await expect(toggle).toHaveText("Theme: Auto");
  const root = page.locator("html");
  await expect(root).not.toHaveAttribute("data-theme", /.+/);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Light");
  await expect(root).toHaveAttribute("data-theme", "light");
  // The desk itself re-themes: the page ground is light.
  const ground = await page.locator("qd-app-root").evaluate((el) => getComputedStyle(el).backgroundColor);
  const [red, green, blue] = ground.match(/\d+/g)!.map(Number);
  expect(red + green + blue).toBeGreaterThan(600);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Dark");
  await expect(root).not.toHaveAttribute("data-theme", /.+/);
  await toggle.click();
  await expect(toggle).toHaveText("Theme: Auto");
  await context.close();
});

test("Auto follows the system's light setting", async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: "light" });
  const page = await context.newPage();
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
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
