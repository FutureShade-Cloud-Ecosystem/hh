// UX-128 round 3 (P3-5): the Quote Desk on a dealer's own box. No session reads the sign in line, a core that answers
// 503 reads the offline line, and neither one says a word of demo roles. The page is served a console config and the
// console answers through routes (every value is fake); the dev server serves the app at the origin root, so the
// front door hop (a prefixed build only) does not move the page and the line stays to be read.
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

test("a console box with no session reads the sign in line, never the demo roles", async ({ page }) => {
  await serveConsole(page, 401);
  await page.goto("/");
  const line = page.locator("p.notice");
  await expect(line).toContainText("Sign in to open Quote Desk.");
  await expect(line.locator("a.signin-link")).toHaveAttribute("href", "/");
  await expect(page.locator("body")).not.toContainText(/demo roles/i);
});

test("a console box whose console answers 503 reads the offline line: no sign in, no demo roles", async ({ page }) => {
  await serveConsole(page, 503);
  await page.goto("/");
  await expect(page.locator("p.notice")).toContainText("Quote Desk will try again on its own.");
  await expect(page.locator("body")).not.toContainText(/demo roles|sign in/i);
});
