// PR 503 round 1 (P1-1): the rail's pin menu is reachable by a real mouse.
// Hover the rail, cross the gap in small steps (the hover must hold), move to
// "Pin wide", click, and assert the rail's pin state (data-pin and property) and the stored value; then
// the same for "Pin as icons". The menu is a fixed popover, so it paints
// beside the rail and is not clipped by it.
import { expect, test } from "@playwright/test";

const SHOTS = process.env.G1_SHOTS_DIR ?? "";

test("hover the rail, cross to Pin wide, click: the rail's pin state (data-pin and property) and the stored value follow", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/test/pw-harness/index.html");
  const rail = page.locator("hh-shell-rail");
  const menu = rail.locator(".pin-menu");
  await expect(menu).toBeHidden();

  const railBox = (await rail.locator("nav.rail").boundingBox())!;
  await page.mouse.move(railBox.x + railBox.width / 2, railBox.y + 100);
  await expect(menu).toBeVisible();
  const wide = rail.getByRole("button", { name: "Pin wide" });
  const menuBox = (await menu.boundingBox())!;
  expect(menuBox.x).toBeGreaterThanOrEqual(railBox.x + railBox.width);
  if (SHOTS !== "") await page.screenshot({ path: `${SHOTS}/rail-pin-menu-1440-charcoal.png` });

  // Cross the gap in 3px steps toward the Pin wide button: the menu stays.
  const target = (await wide.boundingBox())!;
  const tx = target.x + target.width / 2;
  const ty = target.y + target.height / 2;
  await page.mouse.move(tx, ty, { steps: Math.ceil((tx - railBox.width / 2) / 3) });
  await expect(menu).toBeVisible();
  await wide.click();

  await expect(rail.locator("nav.rail")).toHaveAttribute("data-pin", "wide");
  expect(await rail.evaluate((el) => (el as unknown as { pin: string }).pin)).toBe("wide");
  expect(await page.evaluate(() => window.localStorage.getItem("hh-shell.rail-pin"))).toBe("wide");

  const icons = rail.getByRole("button", { name: "Pin as icons" });
  const iconsBox = (await icons.boundingBox())!;
  await page.mouse.move(iconsBox.x + iconsBox.width / 2, iconsBox.y + iconsBox.height / 2, { steps: 4 });
  await icons.click();
  await expect(rail.locator("nav.rail")).toHaveAttribute("data-pin", "icons");
  expect(await page.evaluate(() => window.localStorage.getItem("hh-shell.rail-pin"))).toBe("icons");
});
