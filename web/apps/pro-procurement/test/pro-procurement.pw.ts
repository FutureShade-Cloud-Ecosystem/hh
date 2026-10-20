// Pro Procurement Board in a real browser against the shared tenant stub. Visual references: the sign in screen is
// HH: Pro shell-signin-phone-brand; the frame is shell-frame-phone-light-r2 and shell-selector-phone-light; the
// landing (Today) follows the ref's TodayPage (the morning briefing, the account and the recent orders). Phone first
// (390 wide), then desktop (1440). The legs here cover the shell: the sign in, the frame, the selector, and the
// landing surface's loading, ready, empty, error and refusal states. Each surface's own legs live beside it.
import { test, expect, type Page } from "@playwright/test";
import { join } from "node:path";

const SHOTS = process.env.PRO_SHOT_DIR ?? join("test", "shots");

const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

async function signIn(page: Page, who: string, password = PASSWORD): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test.describe("phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the sign in screen reads like a contractor's, with the dealer's name and nothing technical", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "HH Pro" })).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toContainText("Gable Landscape Supply");
    await expect(page.locator("hh-pro-signin")).toContainText("Use the email your contractor account knows.");
    const text = (await page.locator("hh-pro-signin").innerText()).toLowerCase();
    for (const word of ["console", "core", "token", "session", "exchange", "stub", "demo"]) expect(text).not.toContain(word);
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await page.screenshot({ path: join(SHOTS, "signin-phone.png") });
  });

  test("a wrong password is refused in plain words and the person stays on the sign in screen", async ({ page }) => {
    await signIn(page, "owner", "not-the-password");
    await expect(page.getByTestId("signin-message")).toHaveText("That email and password do not match.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("signin-password")).toHaveValue("");
  });

  test("signing in shows the frame with the dealer, Today as the landing, and the account's recent orders", async ({ page }) => {
    await signIn(page, "owner");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.getByTestId("frame-dealer")).toHaveText("Gable Landscape Supply");
    await expect(page.getByTestId("frame-selector")).toContainText("Pro Procurement");
    await expect(page.getByTestId("surface-title")).toHaveText("Today");
    await expect(page.getByTestId("today-account")).toContainText("Summit Ridge Builders");
    await expect(page.getByTestId("today-rows")).toContainText("SO-30506");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.screenshot({ path: join(SHOTS, "today-phone.png") });
  });

  test("the selector lists only the surfaces this role opens, and signing out returns to the sign in screen", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("button", { name: "Board" })).toBeVisible();
    await page.screenshot({ path: join(SHOTS, "selector-phone.png") });
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await page.getByTestId("frame-sign-out").click();
    await expect(page.locator("hh-pro-signin")).toBeVisible();
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });

  test("nothing a person typed or was given rests in web storage, the address bar or the console", async ({ page }) => {
    const logged: string[] = [];
    page.on("console", (message) => logged.push(message.text()));
    await signIn(page, "owner");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    const stored = await page.evaluate(() => JSON.stringify({ local: { ...window.localStorage }, session: { ...window.sessionStorage }, cookie: document.cookie }));
    expect(stored).not.toMatch(/pro-stub|stub-password|Bearer/);
    expect(page.url()).not.toMatch(/pro-stub|token|password/);
    expect(logged.join("\n")).not.toMatch(/pro-stub|stub-password|Bearer/);
  });

  test("the landing shows a loading state while the server answers", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { delayMs: 900 } });
    await signIn(page, "owner");
    await expect(page.getByTestId("surface-state")).toHaveAttribute("data-state", "loading");
    await expect(page.getByTestId("today-rows")).toBeVisible();
  });

  test("a slow session check and slow data call keep the signed in frame with the loading pattern, never the sign in page", async ({ page }) => {
    await page.route("**/api/v1/tenant/**", async (route) => {
      if (route.request().url().endsWith("/tenant/config")) return route.continue();
      await new Promise((resolve) => setTimeout(resolve, 1800));
      return route.continue();
    });
    await signIn(page, "owner");
    await expect(page.locator("hh-pro-frame")).toBeVisible({ timeout: 600 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    const state = page.getByTestId("surface-state");
    await expect(state).toHaveAttribute("data-state", "loading");
    await expect(state.locator('.state-indicator[data-pattern="progress"]')).toBeVisible();
    await expect(page.getByTestId("today-rows")).toContainText("SO-", { timeout: 6000 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
  });

  test("a network failure on the session check or data call keeps the person in the frame with not reachable words and Try again", async ({ page }) => {
    let failMe = true;
    await page.route("**/api/v1/tenant/me", async (route) => {
      if (failMe) return route.abort("failed");
      return route.continue();
    });
    await signIn(page, "owner");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    const state = page.getByTestId("surface-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Pro is not reachable right now. Try again in a moment.");
    failMe = false;
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("today-rows")).toContainText("SO-");
  });

  test("an empty orders world shows Today's own empty words", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { empty: "orders" } });
    await signIn(page, "owner");
    await expect(page.getByTestId("recent-empty")).toContainText("No orders yet");
  });

  test("a failure shows the error state, and Try again recovers once the server does", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/dashboard", status: 503 } } });
    await signIn(page, "owner");
    const state = page.getByTestId("surface-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Nothing was changed.");
    await request.post("/_stub/reset");
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("today-rows")).toBeVisible();
  });

  test("a server refusal on the landing shows the refusal state with no retry", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/dashboard", status: 403 } } });
    await signIn(page, "owner");
    const state = page.getByTestId("surface-state");
    await expect(state).toHaveAttribute("data-state", "refusal");
    await expect(state).toContainText("Your role does not open this");
    await expect(state.getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("a lapsed session on the landing signs the person out with a plain message, not a role refusal", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/dashboard", status: 401 } } });
    await signIn(page, "owner");
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("surface-state")).toHaveCount(0);
  });

  test("a role that does not hold the landing sees the refusal state and no call is made for it", async ({ page }) => {
    const calls: string[] = [];
    page.on("request", (r) => calls.push(new URL(r.url()).pathname));
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.getByTestId("surface-state")).toHaveAttribute("data-state", "refusal");
    expect(calls).not.toContain("/api/v1/tenant/dashboard");
  });

  test("choosing another app's surface goes to that app's own address", async ({ page }) => {
    await page.route(/^https?:\/\/[^/]+\/pro-(procurement|pay|field)\/$/, (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<title>elsewhere</title>" }));
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='procurement'])").first();
    const target = await elsewhere.getAttribute("data-app");
    await elsewhere.click();
    await page.waitForURL(/\/pro-[a-z]+\/$/);
    expect(new URL(page.url()).pathname).toBe(`/pro-${target}/`);
  });
});

test.describe("desktop", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the frame shows the role beside the sign out and Today fits the page", async ({ page }) => {
    await signIn(page, "owner");
    await expect(page.getByTestId("frame-role")).toBeVisible();
    await expect(page.getByTestId("today-rows")).toBeVisible();
    await page.screenshot({ path: join(SHOTS, "today-desktop.png") });
  });
});
