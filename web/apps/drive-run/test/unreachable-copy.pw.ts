// UX-137: a failed call tells the driver to check their signal only when nothing answered.
// A 503 from the server reads as a server problem; an aborted request reads as the signal.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Page } from "@playwright/test";
import { TINY_PNG, accountItem, driverHandoff, expect, resetStub, test } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const SHOT_DIR = process.env.UX137_SHOT_DIR ?? "";
// The run sheet reads from the offline store, so the direct photo door is the call a stub failure reaches.
const SERVER = "The photo did not send. The server had a problem. Add it again in a moment.";
const SIGNAL = "The photo did not send. Check your signal and add it again.";

test.beforeEach(async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

async function setTheme(page: Page, theme: "light" | "charcoal"): Promise<void> {
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
  const root = page.locator("html");
  const current = (await root.getAttribute("data-theme")) === "light" ? "light" : "charcoal";
  if (current === theme) return;
  await accountItem(page, theme === "charcoal" ? "Brand theme" : "Light theme");
  if (theme === "light") await expect(root).toHaveAttribute("data-theme", "light");
  else await expect(root).not.toHaveAttribute("data-theme", "light");
}

// The stop is taken en route on the page as loaded (the helper loads it), the theme is set on the run
// sheet, and the proof sheet opens from there so the theme survives to the shot.
async function toPhoto(page: Page, theme: "light" | "charcoal"): Promise<void> {
  await page.goto("/");
  await setTheme(page, theme);
  await page.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).waitFor();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
}

async function shoot(page: Page, name: string): Promise<void> {
  if (SHOT_DIR === "") return;
  mkdirSync(SHOT_DIR, { recursive: true });
  await page.screenshot({ path: join(SHOT_DIR, name) });
}

for (const theme of ["light", "charcoal"] as const) {
  test(`a 503 on the photo upload reads as a server problem, not a signal problem (${theme})`, async ({ page }) => {
    await page.route(/\/photos$/, (route) =>
      route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "unavailable" }) }),
    );
    await toPhoto(page, theme);
    await expect(page.getByRole("alert")).toHaveText(SERVER);
    await expect(page.locator("body")).not.toContainText("Check your signal");
    await shoot(page, `server-line-390-${theme}.png`);
  });

  test(`an aborted photo upload reads as a lost signal (${theme})`, async ({ page }) => {
    await page.route(/\/photos$/, (route) => route.abort());
    await toPhoto(page, theme);
    await expect(page.getByRole("alert")).toHaveText(SIGNAL);
    await shoot(page, `signal-line-390-${theme}.png`);
  });
}
