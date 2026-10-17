// The front door's handoff, proved end to end on the Quote Desk's side
// (HH-DEMO-1, the Quote Desk's adopt, decision log rows 893, 899 and 906).
// The front door mints the session and holds it for the box under its own
// session storage key; when a module tile's navigation boots this app, the
// desk adopts that mint and lands signed in with no second picker. The
// stubbed harness cannot run the front door itself (the front door's own
// suite proves the mint and the two keys there), so these tests seed the
// exact record the front door writes, in the exact shape its custody
// validates, and watch this app's boot take it: the bar comes up under the
// role's name, the entry cards never show, the handoff key stays in the
// tab until Sign out or Switch role (a reload or a second visit keeps the
// session, decision log row 1040), and the custody mirror carries the display profile as every other sign in leaves
// it. A record that fails a read is dropped and the entry page with the
// cards comes up, the desk's ordinary way in. Sign out ends an adopted
// session the ordinary way, and the box's Switch role walk (the front door
// clears its key, the visitor picks the other role, the module opens again)
// lands as that other role the same way. The shape is the till's
// (counter-till test/handoff.pw.ts on demo/front-door 385cd8d6).
import { expect, test, type Page } from "@playwright/test";

const HANDOFF_KEY = "hh-demo.front-door.session.v1";
const MIRROR_KEY = "hh-auth.session.v2";

/** A JWT-shaped token naming the stub account, the stub's own mint's shape. */
function tokenFor(userId: string): string {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  return `${segment({ alg: "ES256", typ: "JWT" })}.${segment({
    iss: "hh-demo-stub",
    sub: userId,
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function handoffRecord(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  // The quote routes read the bearer's sub claim, so the seeded handoff must
  // name a demo account for the branch read to land.
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  return {
    token: tokenFor("demo-account-sales"),
    tokenExpiresAtMs: (issuedAtSeconds + 60 * 60) * 1000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
    ...overrides,
  };
}

/** The manager's handoff, the record the front door leaves for the box's Switch role walk. */
function managerHandoffRecord(): Record<string, unknown> {
  return handoffRecord({
    token: tokenFor("demo-account-sales-manager"),
    tokenExpiresAtMs: (Math.floor(Date.now() / 1000) + 60 * 60) * 1000,
    flow: "sales-manager",
    roleLabel: "Sales manager",
    user: {
      userId: "demo-account-sales-manager",
      email: "sales-manager@demo.invalid",
      name: "Sales manager (demo)",
    },
    roleCode: "sales_manager",
    roleName: "Sales Manager",
    tier: 2,
  });
}

/**
 * Seed the front door's key before any app script runs, the handoff's
 * arrival order. The seed is a one-shot: a marker beside the key keeps the
 * init script from re-seeding on a later navigation in the same tab, the
 * way the real storage behaves (the front door writes the record once, and
 * the desk's adopt spends it), so a reload boots over whatever the page
 * itself left there, which is how the Switch role walk's second handoff is
 * staged below.
 */
async function seedHandoff(page: Page, record: unknown): Promise<void> {
  await page.addInitScript(
    ({ key, value }) => {
      const marker = `${key}#seeded`;
      if (window.sessionStorage.getItem(marker) !== null) return;
      window.sessionStorage.setItem(marker, "1");
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(record) },
  );
}

test("a boot from the front door's key lands signed in as the handed role, no second picker", async ({
  page,
}) => {
  await seedHandoff(page, handoffRecord());
  await page.goto("/");
  // The bar is up under the role's name; the entry cards never show.
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toHaveCount(0);
  // The handoff key stays in the tab until Sign out or Switch role (decision log row 1040); custody's
  // mirror is written as every other sign in leaves it.
  const stored = await page.evaluate(
    (keys) => keys.map((key) => window.sessionStorage.getItem(key) !== null),
    [HANDOFF_KEY, MIRROR_KEY],
  );
  expect(stored).toEqual([true, true]);
  // Sign out ends it the ordinary way: the entry cards come back, and the key goes with the session.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
});

test("a reload in the same tab keeps the session, and a second visit to a quote's address does too", async ({ page }) => {
  await seedHandoff(page, handoffRecord());
  await page.goto("/");
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  await page.reload();
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  // A second visit by the address (the module's link opened again in the tab) lands on the desk, not the entry.
  await page.goto("/#/quotes/q-1001");
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
});

test("Switch role drops the key with the session", async ({ page }) => {
  await seedHandoff(page, handoffRecord());
  await page.goto("/");
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  await page.evaluate(() => {
    (window as { marker?: string }).marker = "alive";
  });
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  await page.waitForFunction(() => (window as { marker?: string }).marker === undefined, undefined, { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
});

test("a handoff record that fails a read is dropped and the entry page comes up", async ({ page }) => {
  await seedHandoff(page, { token: "not-a-session" });
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  const stored = await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY);
  expect(stored).toBeNull();
});

test("the box's Switch role walk lands the desk as the other role the same way", async ({ page }) => {
  await seedHandoff(page, handoffRecord());
  await page.goto("/");
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: 15_000 });
  // Sign out is the desk's end of the first walk; the front door's Switch
  // role then leaves a fresh record for the other role, and the module
  // opens again. The re-seed is written from the page side, then the desk
  // boots over it exactly as over the first handoff.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
  await page.evaluate(
    ({ key, value }) => {
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(managerHandoffRecord()) },
  );
  await page.reload();
  await expect(page.getByTestId("header-word")).toHaveText("Sales manager", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toHaveCount(0);
  // The second record is kept for the tab like the first.
  const stored = await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY);
  expect(stored).not.toBeNull();
});
