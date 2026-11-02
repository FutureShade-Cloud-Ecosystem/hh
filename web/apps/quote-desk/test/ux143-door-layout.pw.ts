// UX-143: the sign in door's title sits on the page's left gutter, not near
// the centre. The door comes the way the console spec reaches it: a served
// console config, the console route answering 401 (no session).
import { expect, test, type Page } from "@playwright/test";

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

test("the sign in door's heading is visible and its left edge sits in the page gutter", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await serveConsole(page, 401);
  await page.goto("/");
  const heading = page.locator("h2.headline");
  await expect(heading).toHaveText("Quote Desk");
  await expect(heading).toBeVisible();
  const box = await heading.boundingBox();
  expect(box).not.toBeNull();
  expect(box?.x ?? 9999).toBeGreaterThanOrEqual(0);
  // The gutter, not the centre: the centred stage put the line near x=328.
  expect(box?.x ?? 9999).toBeLessThan(100);
});
