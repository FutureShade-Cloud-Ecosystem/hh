// The till served under /till/ on the box (the demo image's shape): a demo build that finds no
// session sends the person to the front door at the origin root to pick a role, and one that
// finds the front door's key keeps it in the tab until Sign out (decision log row 1040), so a
// reload keeps the session. Run by the base-aware config (test/popout-base-aware.config.ts),
// where BASE_URL is /till/; at the root (the default harness) the entry cards are the way in.
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

/**
 * Seed the front door's key before any app script runs, the handoff's arrival order, once: the
 * marker rides window.name (it survives a reload and a cross-page navigation and is not storage the
 * app clears), so a reload boots over what the page itself left and a later page is never re-seeded.
 */
async function seedHandoff(page: Page, record: unknown): Promise<void> {
  await page.addInitScript(
    ({ key, value }) => {
      if (window.name === "seeded") return;
      window.name = "seeded";
      window.sessionStorage.setItem(key, value);
    },
    { key: HANDOFF_KEY, value: JSON.stringify(record) },
  );
}


/** The box's root stands in for the front door: a plain page, so the redirect has somewhere to land. */
async function standInFrontDoor(page: Page): Promise<void> {
  await page.route((url) => url.pathname === "/", (route) =>
    route.fulfill({ status: 200, contentType: "text/html", body: "<title>front door</title><p>Pick a role</p>" }),
  );
}

test("a demo build with no session sends the person to the front door", async ({ page }) => {
  await standInFrontDoor(page);
  await page.goto("");
  await expect(page).toHaveURL((url) => url.pathname === "/");
  await expect(page.getByText("Pick a role")).toBeVisible();
});

test("a record that fails a read is dropped and the person goes to the front door", async ({ page }) => {
  await standInFrontDoor(page);
  await seedHandoff(page, { token: "not-a-session" });
  await page.goto("");
  await expect(page).toHaveURL((url) => url.pathname === "/");
});

test("the kept key survives a reload under /till/, and Sign out sends the person to the front door", async ({
  page,
}) => {
  await standInFrontDoor(page);
  await seedHandoff(page, handoffRecord());
  await page.goto("");
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales", { timeout: 15_000 });
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key) !== null, HANDOFF_KEY)).toBe(true);
  await page.reload();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales", { timeout: 15_000 });
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page).toHaveURL((url) => url.pathname === "/");
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
});

test("a session the server refuses is dropped and the person goes to the front door, with no loop", async ({
  page,
}) => {
  await standInFrontDoor(page);
  await page.route("**/api/v1/branches**", (route) =>
    route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "session ended" }) }),
  );
  await seedHandoff(page, handoffRecord());
  await page.goto("");
  await expect(page).toHaveURL((url) => url.pathname === "/");
  // The front door's page is the one place left: it sees no key, so it shows the picker, not a signed in role.
  expect(await page.evaluate((key) => window.sessionStorage.getItem(key), HANDOFF_KEY)).toBeNull();
  // Going back to the till finds no key and no session, and lands on the front door again: no ghost adopt.
  await page.goto("/till/");
  await expect(page).toHaveURL((url) => url.pathname === "/");
});
