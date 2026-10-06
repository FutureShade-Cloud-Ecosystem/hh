// The front door's handoff, proved end to end on the till's side (HH-DEMO-1
// D2 polish, item 6). The front door mints the session and holds it for the
// box under its own sessionStorage key; when a module tile's navigation
// boots this app, the till adopts that mint and lands signed in with no
// second picker. The stubbed harness cannot run the front door itself (the
// front door's own suite proves the mint and the two keys there), so these
// tests seed the exact record the front door writes, in the exact shape its
// custody validates, and watch this app's boot take it: the terminal comes
// up under the role's name, the entry cards never show, the handoff key is
// cleared in the same step (a later visit never re-adopts a spent handoff),
// and the custody mirror carries the display profile as every other sign in
// leaves it. A record that fails a read is dropped and the entry page with
// the cards comes up, the till's ordinary way in.
import { expect, test, type Page } from "@playwright/test";

const HANDOFF_KEY = "hh-demo.front-door.session.v1";
const MIRROR_KEY = "hh-auth.session.v2";

function handoffRecord(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  // The token is JWT-shaped with the stub account's sub, the same shape the
  // front door's validated custody stores and the harness's own mint answers:
  // the counter's routes read the bearer's sub claim, so the seeded handoff
  // must name a demo account for the branch read to land.
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${segment({ alg: "ES256", typ: "JWT" })}.${segment({
    iss: "hh-demo-stub",
    sub: "demo-account-sales",
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  return {
    token,
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

/** Seed the front door's key before any app script runs, the handoff's arrival order. */
async function seedHandoff(page: Page, record: unknown): Promise<void> {
  await page.addInitScript(
    ({ key, value }) => {
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
  // The terminal is up under the role's name; the entry cards never show.
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toHaveCount(0);
  // The handoff key is spent in the same step; custody's mirror is written
  // as every other sign in leaves it.
  const stored = await page.evaluate(
    (keys) => keys.map((key) => window.sessionStorage.getItem(key) !== null),
    [HANDOFF_KEY, MIRROR_KEY],
  );
  expect(stored).toEqual([false, true]);
  // Sign out ends it the ordinary way: the entry cards come back.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
});

test("a handoff record that fails a read is dropped and the entry page comes up", async ({ page }) => {
  await seedHandoff(page, { token: "not-a-session" });
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  const stored = await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY);
  expect(stored).toBeNull();
});
