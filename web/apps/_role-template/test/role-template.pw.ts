// The role app template's Playwright leg: boots the app in demo mode, taps
// the sales role card, and verifies the signed-in state renders.
import { test, expect } from "@playwright/test";

test("demo mint and signed-in render", async ({ page }) => {
  await page.goto("/");
  // The app renders the demo entry page.
  await expect(page.locator("hos-app-header")).toBeVisible();
  // Tap the first role card.
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(2);
  await cards.first().click();
  // After mint the signed-in chrome renders.
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("main")).toContainText("Inside Sales");
});

// Decision log row 1858: once a dealer is known its lockup leads and no mark or name of ours is drawn.
const DEALER = "Gable Landscape Supply";

test("the header leads with the served dealer and draws nothing of ours, in both themes", async ({ page }) => {
  await page.route("**/api/v1/tenant/config", (route) =>
    route.fulfill({ json: { id: "t1", dealer_name: DEALER, logo_url: "", primary_color: "", support_email: "", support_phone: "" } }),
  );
  await page.goto("/");
  const lockup = page.locator("hos-app-header [data-testid='dealer-lockup']");
  await expect(lockup).toContainText(DEALER);
  for (const theme of ["light", "dark"] as const) {
    await page.evaluate((t) => (t === "light" ? document.documentElement.setAttribute("data-theme", "light") : document.documentElement.removeAttribute("data-theme")), theme);
    const drawn = await page.locator("hos-app-header").evaluate((el) => (el.shadowRoot?.innerHTML ?? "") + (el.shadowRoot?.textContent ?? ""));
    expect(drawn).not.toContain("Hardscape House");
    expect(drawn).not.toContain("HH ");
    expect(drawn).not.toContain('viewBox="0 0 3000 3000"');
    expect(drawn).toContain(DEALER);
  }
});

test("a failed dealer read draws no product lockup at all", async ({ page }) => {
  await page.route("**/api/v1/tenant/config", (route) => route.fulfill({ status: 500, json: {} }));
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("hos-app-header [data-testid='dealer-lockup']")).toHaveCount(0);
  const drawn = await page.locator("hos-app-header").evaluate((el) => el.shadowRoot?.textContent ?? "");
  expect(drawn).not.toContain("Hardscape House");
  expect(drawn).not.toContain("HH ");
});
