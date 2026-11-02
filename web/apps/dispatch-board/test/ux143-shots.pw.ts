// UX-143: this desk's door shots. The console sign in door (the console
// session check answers 401: nobody is signed in) and the offline door (it
// answers 503), at 1280 and 390. It runs only when UX143_SHOT_DIR names a
// folder (so the suite's own run skips it), and writes there and nowhere else:
//
//   UX143_SHOT_DIR=/some/folder UX143_SHOT_PREFIX=before UX143_SHOT_SUFFIX= npx playwright test ux143-shots
//
// UX143_SHOT_PREFIX is the file name's first word (before or after) and
// UX143_SHOT_SUFFIX its theme tail (empty for the light before shots, -light
// or -charcoal for the after ones). Charcoal is this app's own choice, the theme button in the header: the choice the app stores, which
// a reload drops, so the shot follows the press.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const dir = process.env.UX143_SHOT_DIR ?? "";
const prefix = process.env.UX143_SHOT_PREFIX ?? "after";
const suffix = process.env.UX143_SHOT_SUFFIX ?? "";
test.skip(dir === "", "set UX143_SHOT_DIR to take the UX-143 door shots");

const CONSOLE_CONFIG = {
  mode: "console",
  org: "acme-stone",
  consoleEndpoint: "https://console.invalid/v1",
  consoleProjectId: "acme-project",
  coreBaseUrl: "",
  personas: false,
  personasOpen: false,
  dealer: { name: "Acme Stone" },
};

async function serveConsole(page: Page, status: number): Promise<void> {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => {
    const headers = route.request().headers();
    const cors = {
      "access-control-allow-origin": headers["origin"] ?? "",
      "access-control-allow-credentials": "true",
      "access-control-allow-headers": headers["access-control-request-headers"] ?? "content-type",
      "access-control-allow-methods": "GET, POST, DELETE, OPTIONS",
    };
    return route.request().method() === "OPTIONS"
      ? route.fulfill({ status: 204, headers: cors })
      : route.fulfill({ status, json: { message: "fake" }, headers: cors });
  });
}

async function charcoal(page: Page): Promise<void> {
  const theme = page.locator('button[data-action="theme"]');
  await expect(theme).toBeVisible();
  await theme.click();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.getAttribute("data-theme")))
    .toBe(null);
  await page.keyboard.press("Escape");
}

const WIDTHS = [
  { width: 1280, height: 900 },
  { width: 390, height: 844 },
];

for (const size of WIDTHS) {
  for (const door of ["signin", "offline"] as const) {
    test(`the ${door} door at ${size.width}${suffix === "" ? ", light" : suffix}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await serveConsole(page, door === "signin" ? 401 : 503);
      await page.goto("/");
      if (door === "signin") await page.getByText(/Sign in to open/).waitFor();
      else await page.getByText(/try again on its own/).waitFor();
      await page.waitForLoadState("networkidle");
      if (suffix === "-charcoal") await charcoal(page);
      mkdirSync(dir, { recursive: true });
      await page.screenshot({ path: join(dir, `${prefix}-dispatch-board-${door}-${size.width}${suffix}.png`), fullPage: true });
    });
  }
}
