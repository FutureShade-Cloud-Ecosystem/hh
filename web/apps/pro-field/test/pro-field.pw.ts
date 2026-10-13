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

  test("a lapsed session on the landing signs the person out with a plain message, not a role refusal", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 401 } } });
    await signIn(page, "field");
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("landing-state")).toHaveCount(0);
  });

  test("the selector moves between the order surfaces and each one filters its rows", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await panel.getByRole("button", { name: "Deliveries" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Deliveries");
    const deliveryRows = page.getByTestId("landing-rows").getByTestId("order-row");
    await expect(deliveryRows).not.toHaveCount(0);
    for (const row of await deliveryRows.all()) await expect(row).toContainText("Delivery");
    await page.getByTestId("frame-selector").click();
    await panel.getByRole("button", { name: "Pickups" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Pickups");
    const pickupRows = page.getByTestId("landing-rows").getByTestId("order-row");
    await expect(pickupRows).not.toHaveCount(0);
    for (const row of await pickupRows.all()) {
      const words = (await row.innerText()).toLowerCase();
      expect(words.includes("pickup") || words.includes("will call")).toBe(true);
    }
  });

  test("a row opens the order with its journey and its asked-for day, and back returns to the list", async ({ page }) => {
    await signIn(page, "field");
    const firstRow = page.getByTestId("landing-rows").getByTestId("order-row").first();
    const orderNo = (await firstRow.locator(".mono.name").innerText()).trim();
    await firstRow.click();
    await expect(page.getByTestId("surface-title")).toHaveText("Order");
    await expect(page.getByTestId("order-head")).toContainText(orderNo);
    await expect(page.getByTestId("order-head")).toContainText("Promised for");
    const steps = page.getByTestId("order-timeline").locator(".tstep");
    await expect(steps).toHaveCount(5);
    await expect(page.getByTestId("order-lines")).toContainText("at $");
    await expect(page.getByTestId("order-totals")).toContainText("Tax");
    await page.screenshot({ path: test.info().outputPath("order-phone.png") });
    await page.getByRole("button", { name: "Orders" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await expect(page.getByTestId("surface-title")).toHaveText("Tracking");
  });

  test("the delivery notes open with the order's stored words, and saving them says so", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30503" }).click();
    const input = page.getByTestId("instructions-input");
    await expect(input).toHaveValue("Call the site lead 30 minutes before arrival.");
    await input.fill("Call the site lead 30 minutes before arrival. Gate is on the north fence.");
    await page.getByRole("button", { name: "Save notes" }).click();
    await expect(page.getByTestId("instructions-saved")).toHaveText("Delivery instructions saved.");
    await page.screenshot({ path: test.info().outputPath("instructions-phone.png") });
  });

  test("a delivered order's notes read only, with the reason said", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30502" }).click();
    await expect(page.getByTestId("instructions-note")).toHaveText("No instructions.");
    await expect(page.locator("hh-pro-frame")).toContainText("The truck already left with these notes.");
    await expect(page.getByTestId("instructions-input")).toHaveCount(0);
  });

  test("a role without the notes capability reads them and is told why it cannot change them", async ({ page }) => {
    await signIn(page, "ap");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30503" }).click();
    await expect(page.getByTestId("instructions-note")).toHaveText("Call the site lead 30 minutes before arrival.");
    await expect(page.locator("hh-pro-frame")).toContainText("Your role reads these notes");
    await expect(page.getByRole("button", { name: "Save notes" })).toHaveCount(0);
  });

  test("requesting a return says what the dealer's answer allows, in plain words", async ({ page }) => {
    await signIn(page, "owner");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30501" }).click();
    await expect(page.getByTestId("returns-empty")).toBeVisible();
    await page.getByTestId("request-return").click();
    await expect(page.getByTestId("return-words")).toHaveText("This order cannot be returned through Pro. Ask the dealer to arrange it.");
    await expect(page.getByTestId("return-form")).toHaveCount(0);
  });

  test("a role that cannot commit a return sees no button and still sees the requests list", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30501" }).click();
    await expect(page.getByTestId("returns-empty")).toBeVisible();
    await expect(page.getByTestId("request-return")).toHaveCount(0);
  });

  test("a surface in another app says it opens that app and asks to sign in again, one in this app says nothing", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='field'])");
    expect(await elsewhere.count()).toBeGreaterThan(0);
    for (const button of await elsewhere.all()) {
      const app = await button.getAttribute("data-app");
      const name = app === "pay" ? "Pro Pay" : app === "field" ? "Pro Field" : "Pro Procurement";
      await expect(button.getByTestId("opens-note")).toHaveText(`opens ${name}, sign in again`);
    }
    await expect(panel.locator("button[data-app='field'] [data-testid='opens-note']")).toHaveCount(0);
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
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the frame shows the role beside the sign out and the rows fit the page", async ({ page }) => {
    await signIn(page, "field");
    await expect(page.getByTestId("frame-role")).toBeVisible();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await page.screenshot({ path: test.info().outputPath("landing-desktop.png") });
  });

  test("an order view reads as a page at desk width, with the journey beside its facts", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("landing-rows").getByTestId("order-row").first().click();
    await expect(page.getByTestId("order-head")).toBeVisible();
    await expect(page.getByTestId("order-timeline")).toBeVisible();
    await page.screenshot({ path: test.info().outputPath("order-desktop.png") });
  });
});
