// Round 10 item 7 (rows 1040 and 1092): served under /quote/, a visit with no session goes to the front door at the
// origin root to pick a role, never to the desk's own entry cards; a visit with the front door's key lands signed in
// and keeps the key for the tab. Run by test/front-door-prefixed.config.ts, which serves the desk under /quote/.
import { expect, test, type Page } from "@playwright/test";

async function openAccountMenu(page: Page): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (!(await menu.isVisible())) await page.locator("hh-shell-header button.account").click();
}

const HANDOFF_KEY = "hh-demo.front-door.session.v1";

function record(): Record<string, unknown> {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${segment({ alg: "ES256", typ: "JWT" })}.${segment({ iss: "hh-demo-stub", sub: "demo-account-sales", aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  return {
    token,
    tokenExpiresAtMs: (issuedAtSeconds + 3600) * 1000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  };
}

/** The front door is not served by this harness: the origin root answers a marker page, so the walk is visible. */
async function standInForTheFrontDoor(page: import("@playwright/test").Page): Promise<void> {
  await page.route(/^http:\/\/127\.0\.0\.1:\d+\/$/, (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<title>front door</title><p>front door</p>" }));
}

test("with no session the person is sent to the front door, not the desk's entry cards", async ({ page }) => {
  await standInForTheFrontDoor(page);
  await page.goto("./");
  await expect(page.getByText("front door")).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/");
});

test("a quote's address with no session goes to the front door too", async ({ page }) => {
  await standInForTheFrontDoor(page);
  await page.goto("./#/quotes/q-1001");
  await expect(page.getByText("front door")).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/");
});

test("with the front door's key the desk opens, keeps the key, and a reload still opens it", async ({ page }) => {
  await standInForTheFrontDoor(page);
  await page.addInitScript(
    ({ key, value }) => {
      const marker = `${key}#seeded`;
      if (window.sessionStorage.getItem(marker) !== null) return;
      window.sessionStorage.setItem(marker, "1");
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(record()) },
  );
  await page.goto("./");
  await expect(page.locator("hh-shell-header button.account")).toContainText("Sales (demo)", { timeout: 15_000 });
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key) !== null, HANDOFF_KEY)).toBe(true);
  await page.reload();
  await expect(page.locator("hh-shell-header button.account")).toContainText("Sales (demo)", { timeout: 15_000 });
  expect(new URL(page.url()).pathname).toBe("/quote/");
});

test("Sign out under the prefix drops the key and returns to the front door", async ({ page }) => {
  await standInForTheFrontDoor(page);
  await page.addInitScript(
    ({ key, value }) => {
      const marker = `${key}#seeded`;
      if (window.sessionStorage.getItem(marker) !== null) return;
      window.sessionStorage.setItem(marker, "1");
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(record()) },
  );
  await page.goto("./");
  await expect(page.locator("hh-shell-header button.account")).toContainText("Sales (demo)", { timeout: 15_000 });
  await openAccountMenu(page);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByText("front door")).toBeVisible();
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
});
