// Pro Field in a real browser against the shared tenant stub. Visual references: the sign in screen is
// HH: Pro shell-signin-phone-brand; the frame is shell-frame-phone-light-r2 and shell-selector-phone-light; the
// landing is field-tracking-phone-brand (an order per row with its tracking word). Phone first (390 wide), then desktop. The legs cover the sign in, the frame, the
// selector and the landing surface's loading, ready, empty, error and refusal states.
import { test, expect, type Page } from "@playwright/test";

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
    await page.screenshot({ path: test.info().outputPath("signin-phone.png") });
  });

  test("a wrong password is refused in plain words and the person stays on the sign in screen", async ({ page }) => {
    await signIn(page, "owner", "not-the-password");
    await expect(page.getByTestId("signin-message")).toHaveText("That email and password do not match.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("signin-password")).toHaveValue("");
  });

  test("signing in shows the frame with the dealer, the surface and the landing rows", async ({ page }) => {
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.getByTestId("frame-dealer")).toHaveText("Gable Landscape Supply");
    await expect(page.getByTestId("frame-selector")).toContainText("Tracking");
    await expect(page.getByTestId("surface-title")).toHaveText("Tracking");
    await expect(page.getByTestId("landing-rows")).toContainText("SO-");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.screenshot({ path: test.info().outputPath("landing-phone.png") });
  });

  test("the selector lists only the surfaces this role opens, and signing out returns to the sign in screen", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("button", { name: "Tracking" })).toHaveAttribute("aria-current", "page");
    await page.screenshot({ path: test.info().outputPath("selector-phone.png") });
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await page.getByTestId("frame-sign-out").click();
    await expect(page.locator("hh-pro-signin")).toBeVisible();
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });

  test("nothing a person typed or was given rests in web storage, the address bar or the console", async ({ page }) => {
    const logged: string[] = [];
    page.on("console", (message) => logged.push(message.text()));
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    const stored = await page.evaluate(() => JSON.stringify({ local: { ...window.localStorage }, session: { ...window.sessionStorage }, cookie: document.cookie }));
    expect(stored).not.toMatch(/pro-stub|stub-password|Bearer/);
    expect(page.url()).not.toMatch(/pro-stub|token|password/);
    expect(logged.join("\n")).not.toMatch(/pro-stub|stub-password|Bearer/);
  });

  test("the landing shows a loading state while the server answers", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { delayMs: 900 } });
    await signIn(page, "field");
    await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "loading");
    await expect(page.getByTestId("landing-rows")).toBeVisible();
  });

  test("an empty answer shows this app's own empty words", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { empty: "orders" } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "empty");
    await expect(state).toContainText("No orders to track yet");
  });

  test("a failure shows the error state, and Try again recovers once the server does", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 503 } } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Nothing was changed.");
    await request.post("/_stub/reset");
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
  });

  test("a server refusal shows the refusal state with no retry", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 403 } } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "refusal");
    await expect(state).toContainText("Your role does not open this");
    await expect(state.getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("an unbuilt surface says it is not open yet and the selector moves between them", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const other = panel.locator("button[data-app='field']:not([aria-current='page'])").first();
    const label = (await other.innerText()).trim();
    await other.click();
    await expect(page.getByTestId("surface-title")).toHaveText(label);
    await expect(page.getByTestId("landing-state")).toContainText("This surface is not open yet.");
  });

  test("choosing another app's surface goes to that app's own address", async ({ page }) => {
    await page.route(/^https?:\/\/[^/]+\/pro-(procurement|pay|field)\/$/, (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<title>elsewhere</title>" }));
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='field'])").first();
    const target = await elsewhere.getAttribute("data-app");
    await elsewhere.click();
    await page.waitForURL(/\/pro-[a-z]+\/$/);
    expect(new URL(page.url()).pathname).toBe(`/pro-${target === "procurement" ? "procurement" : target}/`);
  });
});

test.describe("desktop", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("the frame shows the role beside the sign out and the rows fit the page", async ({ page }) => {
    await signIn(page, "field");
    await expect(page.getByTestId("frame-role")).toBeVisible();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await page.screenshot({ path: test.info().outputPath("landing-desktop.png") });
  });
});
