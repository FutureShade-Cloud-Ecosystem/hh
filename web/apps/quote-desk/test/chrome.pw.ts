// The signed-in chrome (visual review round 1, P2-1; reshaped by the shared
// header adoption, item 5 of HH-RD2's polish round 2, decision log row 938):
// the chrome IS the design system's hos-app-header, the front door's shape.
// The desk keeps one bar: the house lockup and the demo badge, the role's
// name centred, Switch role and Sign out right, and the theme control is the
// app's own, inside the bar's named theme slot (the item 5 followup). These
// check the bar's shape, not its pixels.
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
  test(`the shared header carries the lockup, the role and Sign out at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    const bar = page.getByTestId("qd-bar");
    await expect(bar).toBeVisible();
    await expect(page.getByTestId("header-word")).toHaveText("Sales");
    await expect(bar.getByRole("img", { name: "HH Yard, a Hardscape House product" })).toBeVisible();
    const barBox = (await bar.boundingBox())!;
    expect(barBox.x).toBe(0);
    expect(barBox.width).toBe(width);
    const signOut = page.getByRole("button", { name: "Sign out", exact: true });
    await expect(signOut).toHaveClass(/btn-secondary/);
    const signBox = (await signOut.boundingBox())!;
    expect(signBox.height).toBeLessThanOrEqual(40);
    expect(signBox.x + signBox.width).toBeLessThanOrEqual(width - 12);
    // The demo marker is the header element's own note, inside the bar, not a
    // full-bleed fill. At the desktop width it is the one-line slim strip; at
    // the narrow width the element's left zone wraps the banner's copy (the
    // front door's own shape: its zones stack only under 480px), so the
    // one-line bound is a desktop fact and the bar grows with the wrap.
    const demo = bar.getByRole("note");
    await expect(demo).toBeVisible();
    const demoBox = (await demo.boundingBox())!;
    if (width >= 1440) {
      expect(demoBox.height).toBeLessThanOrEqual(36);
      expect(barBox.height).toBeLessThanOrEqual(120);
    }
    expect(demoBox.width).toBeLessThan(width - 24);
    expect(demoBox.x).toBeGreaterThan(0);
    // The theme control rides the header's theme slot, inside the bar and
    // inside the viewport.
    const toggle = page.getByTestId("theme-control");
    await expect(toggle).toBeVisible();
    const toggleBox = (await toggle.boundingBox())!;
    expect(toggleBox.x + toggleBox.width).toBeLessThanOrEqual(width - 12);
    expect(toggleBox.y).toBeGreaterThanOrEqual(barBox.y);
    expect(toggleBox.y + toggleBox.height).toBeLessThanOrEqual(barBox.y + barBox.height + 0.5);
  });
}

// The theme control (visual review round 1, P2-5): the ERP's light look was
// unreachable. The bar's theme slot carries the till's cycle (Auto, Light,
// Dark); the dark sheet is the tokens' default and light is the opt-in
// data-theme attribute.
test("the theme control cycles Auto, Light, Dark on the document root", async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: "dark" });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const toggle = page.getByTestId("theme-control");
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

