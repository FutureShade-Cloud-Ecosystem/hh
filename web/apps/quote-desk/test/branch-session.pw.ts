// The session branch (UX-79): a person whose role is granted two branches opens the Quote Desk from the front door
// with the branch key the front door wrote. Exactly one chosen and granted lands on it; both chosen, none, or a
// forged id asks once inside the desk (a radio group of the granted branches by name and one confirm) and writes
// the choice to the key; a one branch person is unchanged. The core still fences every read: the branches route is
// answered here with the two rows a general manager would be granted, and the key only ever narrows that list.
import { expect, test, type Page } from "@playwright/test";

const HANDOFF_KEY = "hh-demo.front-door.session.v1";
const BRANCH_KEY = "hh.session.branches.v1";
const WEST = { id: "6f1c2b3a-0000-4000-8000-000000000001", code: "PLM", name: "Point Loma" };
const EAST = { id: "6f1c2b3a-0000-4000-8000-000000000002", code: "JUL", name: "Julian" };

function handoffRecord(): Record<string, unknown> {
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
  };
}

/** The front door's two keys, seeded once before any app script runs: the handoff and the branch key. */
async function arrive(page: Page, branchKey: unknown, theme: "light" | "dark" = "light"): Promise<void> {
  await page.addInitScript(
    ({ handoff, handoffValue, branch, branchValue, themeValue }) => {
      if (window.name === "arrived") return;
      window.name = "arrived";
      window.sessionStorage.setItem(handoff, handoffValue);
      window.sessionStorage.setItem("hh-demo.theme.v1", themeValue);
      if (branchValue !== null) window.sessionStorage.setItem(branch, branchValue);
    },
    {
      handoff: HANDOFF_KEY,
      handoffValue: JSON.stringify(handoffRecord()),
      branch: BRANCH_KEY,
      branchValue: branchKey === null ? null : typeof branchKey === "string" ? branchKey : JSON.stringify(branchKey),
      themeValue: JSON.stringify(theme),
    },
  );
}

async function grant(page: Page, rows: unknown[]): Promise<void> {
  await page.route("**/api/v1/branches", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(rows) }),
  );
}

const keyIds = (page: Page) =>
  page.evaluate((key) => {
    const raw = window.sessionStorage.getItem(key);
    return raw === null ? null : (JSON.parse(raw) as { ids: string[] }).ids;
  }, BRANCH_KEY);

const ready = (page: Page) => page.locator("qd-quote-builder");

async function shot(page: Page, name: string): Promise<void> {
  const dir = process.env.UX79_SHOTS;
  if (dir !== undefined && dir !== "") await page.screenshot({ path: `${dir}/${name}.png` });
}

test("a general manager with two grants and one branch chosen lands on it, with no question", async ({ page }) => {
  await grant(page, [WEST, EAST]);
  await arrive(page, { ids: [EAST.id] });
  const seen: string[] = [];
  page.on("request", (request) => seen.push(request.url()));
  await page.goto("/");
  await expect(ready(page)).toBeAttached({ timeout: 15_000 });
  await expect(page.getByTestId("branch-chooser")).toHaveCount(0);
  await expect.poll(() => seen.some((url) => url.includes(EAST.id)), { timeout: 10_000 }).toBe(true);
});

test("both branches chosen: the desk asks once inside, names the granted branches, and lands on the pick", async ({ page }) => {
  await grant(page, [WEST, EAST]);
  await arrive(page, { ids: [WEST.id, EAST.id] });
  await page.goto("/");
  const chooser = page.getByTestId("branch-chooser");
  await expect(chooser).toBeVisible({ timeout: 15_000 });
  await expect(ready(page)).toHaveCount(0);
  const group = page.getByRole("radiogroup", { name: /which branch/i });
  const radios = group.getByRole("radio");
  await expect(radios).toHaveCount(2);
  await expect(radios.nth(0)).toContainText(WEST.name);
  await expect(radios.nth(1)).toContainText(EAST.name);
  await expect(radios.nth(0)).toHaveAttribute("aria-checked", "false");
  await expect(radios.nth(0)).toHaveAttribute("tabindex", "0");
  await expect(radios.nth(1)).toHaveAttribute("tabindex", "-1");
  await shot(page, "quote-desk-chooser-1440");
  // One confirm with nothing picked says so and invents no branch.
  await page.getByTestId("branch-chooser-confirm").click();
  await expect(page.getByTestId("branch-chooser-nudge")).toHaveText("Pick a branch to continue.");
  await expect(ready(page)).toHaveCount(0);
  expect(await keyIds(page)).toEqual([WEST.id, EAST.id]);
  // Arrows move the pick with the focus.
  await radios.nth(1).focus();
  await page.keyboard.press("ArrowUp");
  await expect(radios.nth(0)).toBeFocused();
  await expect(radios.nth(0)).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("ArrowDown");
  await expect(radios.nth(1)).toHaveAttribute("aria-checked", "true");
  await radios.nth(0).click();
  await page.getByTestId("branch-chooser-confirm").click();
  await expect(ready(page)).toBeAttached({ timeout: 15_000 });
  await expect(chooser).toHaveCount(0);
  expect(await keyIds(page)).toEqual([WEST.id]);
  // A reload reads the written choice and does not ask again.
  await page.reload();
  await expect(ready(page)).toBeAttached({ timeout: 15_000 });
  await expect(page.getByTestId("branch-chooser")).toHaveCount(0);
});

test("no key, an empty key, a forged id and a damaged key all ask; the forged id is never used", async ({ page }) => {
  for (const key of [null, { ids: [] }, { ids: ["not-a-granted-branch"] }, "not json"]) {
    const fresh = await page.context().newPage();
    await grant(fresh, [WEST, EAST]);
    await arrive(fresh, key);
    await fresh.goto("/");
    await expect(fresh.getByTestId("branch-chooser")).toBeVisible({ timeout: 15_000 });
    await expect(ready(fresh)).toHaveCount(0);
    await fresh.close();
  }
});

test("a one branch person is unchanged: the one granted branch is taken, whatever the key holds", async ({ page }) => {
  await arrive(page, { ids: ["not-a-granted-branch"] });
  await page.goto("/");
  await expect(ready(page)).toBeAttached({ timeout: 15_000 });
  await expect(page.getByTestId("branch-chooser")).toHaveCount(0);
});

test("no granted branch at all keeps the refusal in words, and no chooser", async ({ page }) => {
  await grant(page, []);
  await arrive(page, { ids: [WEST.id] });
  await page.goto("/");
  await expect(page.getByText(/did not start: the branch list did not answer exactly the one branch/i)).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("branch-chooser")).toHaveCount(0);
});

for (const theme of ["light", "dark"] as const) {
  for (const [width, height] of [[1440, 900], [390, 844]] as const) {
    test(`the in desk chooser fits and meets 44 px on a phone: ${theme} ${width}x${height}`, async ({ page }) => {
      await page.setViewportSize({ width, height });
      await grant(page, [WEST, EAST]);
      await arrive(page, { ids: [WEST.id, EAST.id] }, theme);
      await page.goto("/");
      await expect(page.getByTestId("branch-chooser")).toBeVisible({ timeout: 15_000 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
      const radios = page.getByRole("radiogroup", { name: /which branch/i }).getByRole("radio");
      await radios.nth(1).click();
      for (const control of [radios.nth(0), radios.nth(1), page.getByTestId("branch-chooser-confirm")]) {
        const box = await control.boundingBox();
        expect(box!.height).toBeGreaterThanOrEqual(44);
      }
      await shot(page, `quote-desk-chooser-${theme}-${width}`);
    });
  }
}
