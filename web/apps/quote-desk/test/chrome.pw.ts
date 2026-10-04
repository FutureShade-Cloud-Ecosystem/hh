// The signed-in chrome (visual review round 1, P2-1): the entry page had a top
// bar and every signed-in screen dropped it. The desk now keeps one bar: the
// mark and title, the demo as a slim strip, the role label and Sign out as a
// proper secondary button. These check the bar's shape, not its pixels.
import { expect, test, type Page } from "@playwright/test";

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
