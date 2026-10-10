// The front door's acceptance spec: the seven behaviours the D2 brief names,
// walked in a real browser against the stubbed harness. The coming labels are
// written here rather than read from the app, so a catalog that forgets a
// role fails here and not in front of a visitor (the till's entry test's own
// rule).
//
// The mint the stub answers carries a JWT-shaped token whose exp claim is one
// hour out, so the app's validated custody adopts it for real; the token
// clearing the brief asks the suite to prove is read straight out of
// sessionStorage, both of the app's keys.
import { expect, test, type Page } from "@playwright/test";

const COMING_LABELS = [
  "Outside Sales",
  "Yard Team",
  "Yard Manager",
  "Logistics Manager",
  "Branch Manager",
  "Procurement Manager",
  "Payables and Receivables",
  "Financial Controller",
  "General Manager",
  "HR",
];

const SESSION_KEY = "hh-demo.front-door.session.v1";
const MIRROR_KEY = "hh-auth.session.v2";

async function storedKeys(page: Page): Promise<string[]> {
  return page.evaluate((keys) => keys.filter((key) => window.sessionStorage.getItem(key) !== null), [
    SESSION_KEY,
    MIRROR_KEY,
  ]);
}

async function storedFlow(page: Page): Promise<string | null> {
  return page.evaluate((key) => {
    const raw = window.sessionStorage.getItem(key);
    if (raw === null) return null;
    try {
      const parsed = JSON.parse(raw) as { flow?: unknown };
      return typeof parsed.flow === "string" ? parsed.flow : null;
    } catch {
      return null;
    }
  }, SESSION_KEY);
}

async function stubArmsRefusal(page: Page, flow: string): Promise<void> {
  const armed = await page.request.post("/_stub/refuse", {
    data: { status: 429, error: "the demo mint limit is spent for now, wait a moment and tap again", flows: [flow] },
  });
  expect(armed.ok()).toBe(true);
}

async function stubResets(page: Page): Promise<void> {
  const reset = await page.request.post("/_stub/reset");
  expect(reset.ok()).toBe(true);
}

async function signIn(page: Page, label: string): Promise<void> {
  await page.getByRole("button", { name: label, exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await stubResets(page);
  await page.goto("/");
});

test("the picker renders the three live cards and the ten coming rows", async ({ page }) => {
  const sales = page.getByRole("button", { name: "Sales", exact: true });
  await expect(sales).toBeVisible();
  await expect(sales).toBeEnabled();
  // The live cards lead: one line of what the role does and the call to act.
  // The Sales line names both modules the role works (decision log row 900);
  // the Counter Till tile's own line, on the role home, stays about the till.
  await expect(sales).toContainText("Ring up counter sales and build quotes.");
  await expect(sales).toContainText("Sign in as Sales");
  await expect(sales).toContainText("Counter Till");
  await expect(sales).toContainText("Quote Desk");

  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toBeVisible();
  await expect(manager).toBeEnabled();
  await expect(manager).toContainText("The counter plus approvals and margins.");
  await expect(manager).toContainText("Sign in as Sales manager");
  await expect(manager).toContainText("Manager Home");
  await expect(manager).toContainText("Dealer Admin");
  await expect(manager).toContainText("House Messages");

  // The driver's card is live with HH Drive as its first tile: the role's
  // own line, the live HH Drive tile, and the closed House Messages module.
  const driver = page.getByRole("button", { name: "Driver", exact: true });
  await expect(driver).toBeVisible();
  await expect(driver).toBeEnabled();
  await expect(driver).toContainText("The day's run, stop to proof of delivery.");
  await expect(driver).toContainText("Sign in as Driver");
  await expect(driver).toContainText("HH Drive");

  // Coming recedes: the disclosure starts collapsed and opens to today's
  // list, unchanged, its rows disabled with their muted chips.
  const disclosure = page.getByTestId("coming-disclosure");
  await expect(disclosure).toContainText("Ten more roles are coming");
  const coming = page.getByRole("list", { name: "Coming roles" });
  await expect(coming).not.toBeVisible();
  await disclosure.getByText("Ten more roles are coming").click();
  await expect(coming).toBeVisible();
  await expect(coming.getByRole("listitem")).toHaveCount(10);
  for (const label of COMING_LABELS) {
    const row = coming.getByRole("listitem").filter({ hasText: label });
    await expect(row).toBeVisible();
    const control = row.getByRole("button", { name: label, exact: true });
    await expect(control).toBeDisabled();
    await expect(row).toContainText("Coming");
    await expect(row.locator(".chip").first()).toBeVisible();
  }
  // Outside Sales has no till in the decided matrix: the row says so by omission.
  await expect(
    coming.getByRole("listitem").filter({ hasText: "Outside Sales" }),
  ).not.toContainText("Counter Till");
});

test("the header carries the brand, the demo badge and the Pick a role word when not signed in", async ({ page }) => {
  const brand = page.getByRole("img", { name: "HH Yard, a Hardscape House product" });
  await expect(brand).toBeVisible();
  const badge = page.getByRole("note");
  await expect(badge).toBeVisible();
  await expect(badge).toContainText("Demo");
  await expect(badge).toContainText("Everything on this box is invented demo data: nothing here is real.");
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  // The signed-in controls stay off the pre-auth header.
  await expect(page.getByRole("button", { name: "Switch role", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toHaveCount(0);
});

test("a Sales sign in shows the two tiles and mints through the demo org", async ({ page }) => {
  await signIn(page, "Sales");
  // The headline reads naturally: the role's name as the title, the walk's
  // line under it.
  await expect(page.getByTestId("home-title")).toHaveText("Sales");
  await expect(page.getByText("Open a module to walk it", { exact: true })).toBeVisible();
  // The live tiles lead with the call to open.
  const till = page.getByRole("button", { name: "Counter Till", exact: true });
  await expect(till).toBeVisible();
  await expect(till).toContainText("Open the Counter Till");
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Manager Home", exact: true })).toHaveCount(0);
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
  // The mint went out with the demo org on it: the client configuration's own constant.
  const peek = await page.request.get("/_stub/peek");
  expect((await peek.json()).lastOrg).toBe("hh-demo");
  // The session the brief's handoff rule stores is in sessionStorage.
  expect(await storedFlow(page)).toBe("sales");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("a Sales manager sign in shows the five tiles", async ({ page }) => {
  await signIn(page, "Sales manager");
  for (const name of ["Counter Till", "Quote Desk", "Manager Home", "Dealer Admin", "House Messages"]) {
    await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  }
  // The manager's not-yet modules recede under their own word.
  await expect(page.getByRole("heading", { name: "Coming next" })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Sales manager");
  expect(await storedFlow(page)).toBe("sales-manager");
});

test("a Driver sign in shows the HH Drive tile, which navigates to /drive/", async ({ page }) => {
  await signIn(page, "Driver");
  // The driver's two tiles: HH Drive (live, opens /drive/) and House Messages
  // (closed, the polite coming-soon page).
  const drive = page.getByRole("button", { name: "HH Drive", exact: true });
  await expect(drive).toBeVisible();
  await expect(drive).toContainText("Open the HH Drive");
  await expect(page.getByRole("button", { name: "House Messages", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Driver");
  expect(await storedFlow(page)).toBe("driver");
  // The HH Drive tile carries the box's /drive/ prefix, the same one the
  // deploy image serves HH Drive under (deploy/demo/Dockerfile line 62).
  // The stub harness here serves the front door alone; the tile's href is
  // what proves the navigation target, without taking the user to a 404.
  await expect(drive).toHaveAttribute("href", "/drive/");
});

test("Switch role returns to the picker and clears the token", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  expect(await storedKeys(page)).toEqual([]);
});

test("Sign out returns to the picker and clears the token", async ({ page }) => {
  await signIn(page, "Sales");
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  expect(await storedKeys(page)).toEqual([]);
});

test("a manager sign in, a Switch role, then a Sales sign in leaves the manager's roles out of Sales", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  expect(await storedKeys(page)).toEqual([]);
  await signIn(page, "Sales");
  expect(await storedFlow(page)).toBe("sales");
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toBeVisible();
  for (const name of ["Manager Home", "Dealer Admin", "House Messages"]) {
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
  }
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});

test("a refused mint speaks in its own card, verbatim, and both cards keep working", async ({ page }) => {
  await stubArmsRefusal(page, "sales-manager");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toContainText(
    "Demo sign-in refused: the demo mint limit is spent for now, wait a moment and tap again",
  );
  expect(await storedKeys(page)).toEqual([]);
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  // The card that refused stays tappable, and the other card never stopped working.
  await expect(manager).toBeEnabled();
  await signIn(page, "Sales");
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});

test("the manager's not-yet modules open the coming-soon page and back again", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: "Dealer Admin", exact: true }).click();
  await expect(page.getByRole("heading", { name: /Dealer Admin/ })).toBeVisible();
  await expect(page.getByText("not on this demo box yet")).toBeVisible();
  // The header stays: the demo badge, the role's name, the two controls.
  await expect(page.getByTestId("header-word")).toHaveText("Sales manager");
  await page.getByRole("button", { name: "Back to Sales manager" }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
});

test("a reload in the same tab resumes the role home from the stored session", async ({ page }) => {
  await signIn(page, "Sales");
  await page.reload();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});
