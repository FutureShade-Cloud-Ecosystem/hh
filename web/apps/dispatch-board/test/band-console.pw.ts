// UX-127: the words "Demo data only." belong to a demo page; a dealer's own box does not carry them.
import { expect, test } from "@playwright/test";

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

test("a console box's page copy drops the demo-data words", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Demo data only");
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\.$/);
  // UX-127 read P2-1: the header's demo banner belongs to a demo page too.
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("hos-app-header")).not.toHaveAttribute("demo", "");
  await expect(page.locator("hos-app-header .demo-banner")).toHaveCount(0);
});

test("a page served no console config still says demo data only", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  await expect(page.locator("h1").first()).toBeVisible();
  await expect(page.locator("p.dim").first()).toHaveText(/[a-z]\. Demo data only\.$/);
  await expect(page.locator("hos-app-header")).toHaveAttribute("demo", "");
  await expect(page.locator("hos-app-header .demo-banner")).toHaveCount(1);
});

// UX-128 round 3 (P3-6): the door of a dealer's own box with no session reads like the other apps' doors: the header
// does not say "Pick a role" (a demo picker word) and the sign in line is not drawn as a dashed scan stage.
test("a console box's door says to sign in: no Pick a role header, no dashed stage box around the line", async ({ page }) => {
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
      : route.fulfill({ status: 401, json: { message: "no session" }, headers: cors });
  });
  await page.goto("/");
  await expect(page.locator("p.notice")).toContainText("Sign in to open Dispatch Board.");
  await expect(page.getByTestId("header-word")).not.toHaveText(/pick a role/i);
  await expect(page.getByTestId("header-word")).toHaveText("Sign in");
  await expect(page.locator("div.stage")).toHaveCSS("border-top-style", "none");
});

// UX-131: the header word follows the door's state. Offline, the body says the server cannot be reached, so the
// header does not say "Sign in".
test("a console box's offline door has no Sign in word in the header", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto("/");
  await expect(page.locator("p.notice")).toContainText(/reach the server/i);
  await expect(page.locator("hos-app-header")).not.toHaveAttribute("word", "Sign in");
  await expect(page.getByTestId("header-word")).toHaveText("Offline");
});

// UX-131 round 2: the header word follows the offline reason. "Offline" is true only for a lost signal; a 503 means
// the server is up but not answering, a 429 means it is busy.
function consoleAnswering(status: number) {
  return (route: import("@playwright/test").Route) => {
    const headers = route.request().headers();
    const cors = {
      "access-control-allow-origin": headers["origin"] ?? "",
      "access-control-allow-credentials": "true",
      "access-control-allow-headers": headers["access-control-request-headers"] ?? "content-type",
      "access-control-allow-methods": "GET, POST, DELETE, OPTIONS",
    };
    return route.request().method() === "OPTIONS"
      ? route.fulfill({ status: 204, headers: cors })
      : route.fulfill({ status, json: { message: "later" }, headers: cors });
  };
}

test("a console box's door answered 503 says the server is unavailable, not offline", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", consoleAnswering(503));
  await page.goto("/");
  await expect(page.locator("p.notice")).toContainText(/not answering/i);
  await expect(page.getByTestId("header-word")).toHaveText("Server unavailable");
});

test("a console box's door answered 429 says the server is busy, not offline", async ({ page }) => {
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", consoleAnswering(429));
  await page.goto("/");
  await expect(page.locator("p.notice")).toContainText(/busy/i);
  await expect(page.getByTestId("header-word")).toHaveText("Server busy");
});
