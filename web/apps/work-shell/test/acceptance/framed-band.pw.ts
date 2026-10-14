// One header band (item 8d, row 1520): a desk app framed by the Work shell shows
// NO header band of its own, and shows it whole when it stands alone. Runs
// only when WS_DIST names an assembled dist (the REAL desk bundles; bash
// tauri/hh-work/assemble-dist.sh, then WS_DIST=<that dist> npx playwright
// test), because the band lives in the real apps: the fixture pages of the
// composed dist have none. One leg per desk app, each proving the three
// states: standalone (band present), framed by the host (band absent, the
// host's band the only one), and the flag in a place it must not work (a
// top level page: band present; a parent at another origin is covered by the pure
// origin check in the design system's framed.test.mjs, because a frame under another origin
// has no session storage of the demo's and cannot boot a desk app).
import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const assembled = (process.env["WS_DIST"] ?? "") !== "";

test.skip(!assembled, "WS_DIST names an assembled dist for this leg");

/**
 * The seven desk apps of the Work profile: the app id, its serve path, its name, and a front door role whose
 * tiles open it standalone (the door writes the session the app reads, as on the demo box).
 */
const APPS = [
  ["counter-till", "/till/", "Counter Till", "Sales manager"],
  ["quote-desk", "/quote/", "Quote Desk", "Sales"],
  ["dispatch-board", "/dispatch/", "Dispatch", "Logistics Manager"],
  ["buying-and-stock", "/buying-and-stock/", "Buying and Stock", "Yard manager"],
  ["money-desk", "/money-desk/", "Money Desk", "Financial Controller"],
  ["manager-home", "/home/", "Manager Home", "Sales manager"],
  ["dealer-admin", "/dealer-admin/", "Dealer Admin", "Sales manager"],
] as const;

const FLAG = "hh-framed=work-shell";

/** The brand lockup's mark: every desk app's own band wears one (the shared header's and the till's own). */
const BAND_MARK = ".brand-mark";

async function signInAs(page: Page, shellUrl: string, label: string): Promise<void> {
  await page.goto(`${shellUrl}/`);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: label, exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
}

/** Standalone, the way the demo box serves it: sign in at the front door's own page, then open the app at the top level. */
async function openStandalone(page: Page, shellUrl: string, role: string, address: string): Promise<void> {
  await page.goto(`${shellUrl}/door/`);
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.locator("[data-href]").first()).toBeVisible();
  await page.goto(`${shellUrl}${address}`, { waitUntil: "domcontentloaded" });
}

async function openApp(page: Page, appId: string): Promise<void> {
  await page.locator("hh-shell-header button.selector").click();
  await page.locator(`hh-shell-header .panel .mi[data-app="${appId}"]`).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", appId);
}

for (const [id, path, name, role] of APPS) {
  test(`${name}: standalone it wears its own band; framed by the Work shell it wears none`, async ({ page, shellUrl }) => {
    // Standalone: the band is there (the brand lockup, the Demo badge).
    const standalone = await page.context().newPage();
    await openStandalone(standalone, shellUrl, role, path);
    await expect(standalone.locator(BAND_MARK).first()).toBeVisible();
    await expect(standalone.getByText("Everything on this box is invented demo data").first()).toBeVisible();
    await standalone.close();

    // The flag on a top level page enables nothing: the band stays.
    const topLevel = await page.context().newPage();
    await openStandalone(topLevel, shellUrl, role, `${path}?${FLAG}`);
    await expect(topLevel.locator(BAND_MARK).first()).toBeVisible();
    await topLevel.close();

    // Framed by the host: no band inside the frame, and the host's band is the one band on screen.
    await signInAs(page, shellUrl, "Sales manager");
    await openApp(page, id);
    const frameEl = page.locator('[data-testid="app-frame"]');
    // the frame's address is the app's path plus the flag (the host writes it, the app reads it once)
    await expect.poll(async () => (await frameEl.evaluate((f) => (f as HTMLIFrameElement).contentWindow?.location.search ?? "")) ?? "").toContain(FLAG);
    const frame = page.frameLocator('[data-testid="app-frame"]');
    // wait for the app's own root to stand, so "absent" is judged on a booted page
    await expect
      .poll(async () =>
        frameEl.evaluate((f) => {
          const doc = (f as HTMLIFrameElement).contentDocument;
          return doc?.body?.children.length ?? 0;
        }),
      )
      .toBeGreaterThan(0);
    await page.waitForTimeout(1200);
    await expect(frame.locator(BAND_MARK)).toHaveCount(0);
    await expect(frame.getByText("Everything on this box is invented demo data")).toHaveCount(0);
    await expect(frame.getByRole("button", { name: "Sign out" })).toHaveCount(0);
    await expect(frame.getByText("HH Yard")).toHaveCount(0);
    await expect(page.locator("hh-shell-header")).toBeVisible();
    await expect(page.locator("hh-shell-header")).toContainText("Gable Landscape Supply");
  });
}

test("the shell's account menu carries Theme and Feedback for the app it frames, and the app acts on them", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales manager");
  await openApp(page, "quote-desk");
  const frame = page.frameLocator('[data-testid="app-frame"]');
  await expect(frame.locator("hh-feedback")).toHaveCount(1);
  await page.waitForTimeout(1200);
  const frameTheme = (): Promise<string | null> =>
    page.locator('[data-testid="app-frame"]').evaluate((f) => (f as HTMLIFrameElement).contentDocument?.documentElement.getAttribute("data-theme") ?? null);
  const hostTheme = (): Promise<string | null> => page.evaluate(() => document.documentElement.getAttribute("data-theme"));
  const before = await frameTheme();
  await page.locator("hh-shell-header button.account").click();
  const menu = page.getByTestId("account-menu");
  await expect(menu.getByRole("button", { name: /Theme/ })).toBeVisible();
  await expect(menu.getByRole("button", { name: "Feedback" })).toBeVisible();
  await menu.getByRole("button", { name: /Theme/ }).click();
  await expect.poll(frameTheme).not.toBe(before);
  // the host follows the app's choice, so the two never disagree
  await expect.poll(hostTheme).toBe(await frameTheme());
  await page.locator("hh-shell-header button.account").click();
  await page.getByTestId("account-menu").getByRole("button", { name: "Feedback" }).click();
  await expect(frame.getByRole("heading", { name: /feedback/i }).first()).toBeVisible();
});

test("an app with no Feedback control gets no Feedback button in the shell's menu", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales manager");
  await openApp(page, "manager-home");
  await page.waitForTimeout(1200);
  await page.locator("hh-shell-header button.account").click();
  const menu = page.getByTestId("account-menu");
  await expect(menu.getByRole("button", { name: /Theme/ })).toBeVisible();
  await expect(menu.getByRole("button", { name: "Feedback" })).toHaveCount(0);
});

test("the shell's band carries the Demo badge and the tenant, never another brand", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales manager");
  await expect(page.locator("hh-shell-header")).toContainText("Gable Landscape Supply");
  await expect(page.locator("hh-shell-header")).toContainText("Demo");
  await expect(page.locator("hh-shell-header")).not.toContainText("HH Yard");
});

test("the rail's first control is a visible, named toggle at desktop and tablet widths", async ({ page, shellUrl }) => {
  for (const [width, height] of [
    [1280, 800],
    [834, 1194],
  ] as const) {
    await page.setViewportSize({ width, height });
    await signInAs(page, shellUrl, "Sales manager");
    const toggle = page.locator("hh-shell-rail button.toggle");
    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveAccessibleName(/pins/i);
    const box = await toggle.boundingBox();
    expect(box?.width ?? 0).toBeGreaterThanOrEqual(44);
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
    const glyph = await page.locator("hh-shell-rail button.toggle svg").boundingBox();
    expect(glyph?.width ?? 0).toBeGreaterThanOrEqual(20);
    await expect(page.locator("hh-shell-rail nav.rail")).toHaveAttribute("data-collapsed", "true");
  }
});
