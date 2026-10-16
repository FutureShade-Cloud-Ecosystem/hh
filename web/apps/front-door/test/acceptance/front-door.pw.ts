// The front door's acceptance spec: the seven behaviours the D2 brief names,
// plus the Yard Team card's live leg and the driver's, walked in a real browser against the
// stubbed harness. The card labels are written here rather than read from
// the app, so a catalog that forgets a role fails here and not in front of a
// visitor (the till's entry test's own rule).
//
// The mint the stub answers carries a JWT-shaped token whose exp claim is one
// hour out, so the app's validated custody adopts it for real; the token
// clearing the brief asks the suite to prove is read straight out of
// sessionStorage, both of the app's keys.
import { expect, test, type Page } from "@playwright/test";

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
    data: { status: 429, error: "too many sign-ins just now, wait a moment and tap again", flows: [flow] },
  });
  expect(armed.ok()).toBe(true);
}

async function stubResets(page: Page): Promise<void> {
  const reset = await page.request.post("/_stub/reset");
  expect(reset.ok()).toBe(true);
}

async function signIn(page: Page, label: string): Promise<void> {
  await page.getByRole("button", { name: label, exact: true }).click();
  await expect(page.getByTestId("home-title")).toHaveText(label);
}

test.beforeEach(async ({ page }) => {
  await stubResets(page);
  await page.goto("/");
});

test("the picker renders the live cards and the coming rows", async ({ page }) => {
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
  await expect(sales).toContainText("House Messages");

  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toBeVisible();
  await expect(manager).toBeEnabled();
  await expect(manager).toContainText("The counter plus approvals and margins.");
  await expect(manager).toContainText("Sign in as Sales manager");
  await expect(manager).toContainText("Manager Home");
  await expect(manager).toContainText("Dealer Admin");
  await expect(manager).toContainText("House Messages");

  // The yard manager's card is live with the first Manager Home slice: its
  // own line, the live Manager Home tile, the Yard Pick app's live tile,
  // Buying and Stock's live tile, and the two modules the matrix gives the
  // role that this box does not serve yet, never a silent omission.
  const yardManager = page.getByRole("button", { name: "Yard manager", exact: true });
  await expect(yardManager).toBeVisible();
  await expect(yardManager).toBeEnabled();
  await expect(yardManager).toContainText("The yard: picks, shortages and deliveries.");
  await expect(yardManager).toContainText("Sign in as Yard manager");
  await expect(yardManager).toContainText("Manager Home");
  await expect(yardManager).toContainText("Yard Pick");

  const yard = page.getByRole("button", { name: "Yard Team", exact: true });
  await expect(yard).toBeVisible();
  await expect(yard).toBeEnabled();
  await expect(yard).toContainText("Pick and stage the orders dispatched to the yard.");
  await expect(yard).toContainText("Sign in as Yard Team");
  await expect(yard).toContainText("Yard Pick");
  await expect(yard).toContainText("House Messages");

  const payRec = page.getByRole("button", { name: "Payables and Receivables", exact: true });
  await expect(payRec).toBeVisible();
  await expect(payRec).toBeEnabled();
  await expect(payRec).toContainText("Chase overdue accounts and work the bounced payments.");
  await expect(payRec).toContainText("Sign in as Payables and Receivables");
  await expect(payRec).toContainText("Money Desk");

  const buyer = page.getByRole("button", { name: "Procurement manager", exact: true });
  await expect(buyer).toBeVisible();
  await expect(buyer).toBeEnabled();
  await expect(buyer).toContainText("Buy the branch's stock and track what is incoming.");
  await expect(buyer).toContainText("Sign in as Procurement manager");
  await expect(buyer).toContainText("Buying and Stock");
  await expect(buyer).toContainText("Manager Home");
  await expect(buyer).toContainText("House Messages");

  // The driver's card is live with HH Drive as its first tile: the role's
  // own line, the live HH Drive tile, and the closed House Messages module.
  const driver = page.getByRole("button", { name: "Driver", exact: true });
  await expect(driver).toBeVisible();
  await expect(driver).toBeEnabled();
  await expect(driver).toContainText("The day's run, stop to proof of delivery.");
  await expect(driver).toContainText("Sign in as Driver");
  await expect(driver).toContainText("HH Drive");

  const controller = page.getByRole("button", { name: "Financial Controller", exact: true });
  await expect(controller).toBeVisible();
  await expect(controller).toBeEnabled();
  await expect(controller).toContainText("Sign in as Financial Controller");
  await expect(controller).toContainText("Money Desk");

  // The General Manager's card is live: the Money Desk, Manager Home and
  // Dealer Admin open, House Messages is not built yet and reads as a chip.
  const general = page.getByRole("button", { name: "General Manager", exact: true });
  await expect(general).toBeVisible();
  await expect(general).toBeEnabled();
  await expect(general).toContainText("Sign in as General Manager");
  await expect(general).toContainText("Money Desk");
  await expect(general).toContainText("Manager Home");
  await expect(general).toContainText("Dealer Admin");

  // HR's card is live: House Messages
  // reads as a chip, never as a link, and Dealer Admin opens.
  const hr = page.getByRole("button", { name: "HR", exact: true });
  await expect(hr).toBeVisible();
  await expect(hr).toBeEnabled();
  await expect(hr).toContainText("Sign in as HR");
  await expect(hr).toContainText("House Messages");
  await expect(hr).toContainText("Dealer Admin");

  // The Branch Manager's card is live: the Counter Till, Quote Desk,
  // Dispatch and Load Building and Manager Home open, House Messages reads as a
  // chip.
  const branch = page.getByRole("button", { name: "Branch Manager", exact: true });
  await expect(branch).toBeVisible();
  await expect(branch).toBeEnabled();
  await expect(branch).toContainText("Sign in as Branch Manager");
  await expect(branch).toContainText("Counter Till");
  await expect(branch).toContainText("Dispatch and Load Building");

  // The Logistics Manager's card is live with the dispatch board as its first tile.
  const logistics = page.getByRole("button", { name: "Logistics Manager", exact: true });
  await expect(logistics).toBeVisible();
  await expect(logistics).toBeEnabled();
  await expect(logistics).toContainText("Build the day's loads and send the trucks out.");
  await expect(logistics).toContainText("Sign in as Logistics Manager");
  await expect(logistics).toContainText("Dispatch and Load Building");
  await expect(logistics).toContainText("Manager Home");

  // Outside Sales' card is live: the Quote Desk opens, House Messages reads as
  // a chip, and no till is offered (the matrix gives the role none).
  const outside = page.getByRole("button", { name: "Outside Sales", exact: true });
  await expect(outside).toBeVisible();
  await expect(outside).toBeEnabled();
  await expect(outside).toContainText("Sign in as Outside Sales");
  await expect(outside).toContainText("Quote Desk");
  await expect(outside).toContainText("House Messages");
  await expect(outside).not.toContainText("Counter Till");

  // Every role of the matrix is live, so nothing is left to recede: the coming
  // disclosure is not drawn at all.
  await expect(page.getByTestId("coming-disclosure")).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Coming roles" })).toHaveCount(0);
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
  // All five are on the box (Dealer Admin at /dealer-admin/, House Messages at
  // /messages/), so nothing recedes under the coming-next word.
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "House Messages", exact: true })).toHaveAttribute("data-href", "/messages/");
  await expect(page.getByRole("button", { name: "Dealer Admin", exact: true })).toHaveAttribute("data-href", "/dealer-admin/");
  await expect(page.getByTestId("header-word")).toHaveText("Sales manager");
  expect(await storedFlow(page)).toBe("sales-manager");
});

// L-card for the coming roles cycle: the role signs in from its card and the
// home shows exactly the matrix tiles, each live tile on its own prefix and
// each tile whose app is not on the box yet with no link (it reads "coming").
test("a General Manager sign in shows the matrix tiles, all four live", async ({ page }) => {
  await signIn(page, "General Manager");
  await expect(page.getByTestId("header-word")).toHaveText("General Manager");
  for (const [name, href] of [
    ["Money Desk", "/money-desk/"],
    ["Manager Home", "/home/"],
    ["House Messages", "/messages/"],
    ["Dealer Admin", "/dealer-admin/"],
  ] as const) {
    const tile = page.getByRole("button", { name, exact: true });
    await expect(tile).toBeVisible();
    await expect(tile).toHaveAttribute("data-href", href);
  }
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  expect(await storedFlow(page)).toBe("general-manager");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("an HR sign in shows Dealer Admin and House Messages live", async ({ page }) => {
  await signIn(page, "HR");
  await expect(page.getByTestId("header-word")).toHaveText("HR");
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  const admin = page.getByRole("button", { name: "Dealer Admin", exact: true });
  await expect(admin).toBeVisible();
  await expect(admin).toHaveAttribute("data-href", "/dealer-admin/");
  const messages = page.getByRole("button", { name: "House Messages", exact: true });
  await expect(messages).toBeVisible();
  await expect(messages).toHaveAttribute("data-href", "/messages/");
  expect(await storedFlow(page)).toBe("hr");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("a Branch Manager sign in shows the matrix tiles, all five live", async ({ page }) => {
  await signIn(page, "Branch Manager");
  await expect(page.getByTestId("header-word")).toHaveText("Branch Manager");
  for (const [name, href] of [
    ["Counter Till", "/till/"],
    ["Quote Desk", "/quote/"],
    ["Dispatch and Load Building", "/dispatch/"],
    ["Manager Home", "/home/"],
    ["House Messages", "/messages/"],
  ] as const) {
    const tile = page.getByRole("button", { name, exact: true });
    await expect(tile).toBeVisible();
    await expect(tile).toHaveAttribute("data-href", href);
  }
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  expect(await storedFlow(page)).toBe("branch-manager");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("a Logistics Manager sign in shows the matrix tiles, all three live", async ({ page }) => {
  await signIn(page, "Logistics Manager");
  await expect(page.getByTestId("header-word")).toHaveText("Logistics Manager");
  for (const [name, href] of [
    ["Dispatch and Load Building", "/dispatch/"],
    ["Manager Home", "/home/"],
    ["House Messages", "/messages/"],
  ] as const) {
    const tile = page.getByRole("button", { name, exact: true });
    await expect(tile).toBeVisible();
    await expect(tile).toHaveAttribute("data-href", href);
  }
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  expect(await storedFlow(page)).toBe("logistics-manager");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("an Outside Sales sign in shows the Quote Desk and House Messages tiles and no till", async ({ page }) => {
  await signIn(page, "Outside Sales");
  await expect(page.getByTestId("header-word")).toHaveText("Outside Sales");
  const quote = page.getByRole("button", { name: "Quote Desk", exact: true });
  await expect(quote).toBeVisible();
  await expect(quote).toHaveAttribute("data-href", "/quote/");
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  const messages = page.getByRole("button", { name: "House Messages", exact: true });
  await expect(messages).toBeVisible();
  await expect(messages).toHaveAttribute("data-href", "/messages/");
  expect(await storedFlow(page)).toBe("outside-sales");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("a Yard Team sign in shows the Yard Pick tile and mints the yard flow", async ({ page }) => {
  await page.getByRole("button", { name: "Yard Team", exact: true }).click();
  // The home names the role, and its one live tile is the Yard Pick app.
  const yardTile = page.getByRole("button", { name: "Yard Pick", exact: true });
  await expect(yardTile).toBeVisible();
  await expect(yardTile).toContainText("Open the Yard Pick");
  await expect(page.getByTestId("home-title")).toHaveText("Yard Team");
  await expect(page.getByTestId("header-word")).toHaveText("Yard Team");
  // House Messages is the role's other matrix module and is live on the box:
  // a live tile that opens /messages/, so nothing of this role is coming next.
  await expect(page.getByRole("heading", { name: "Coming next" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "House Messages", exact: true })).toHaveAttribute("data-href", "/messages/");
  expect(await storedFlow(page)).toBe("yard-team");
  expect(await storedKeys(page)).toEqual([SESSION_KEY, MIRROR_KEY]);
});

test("a Driver sign in shows the HH Drive tile, which navigates to /drive/", async ({ page }) => {
  await signIn(page, "Driver");
  // The driver's two tiles: HH Drive (live, opens /drive/) and House Messages
  // (live, opens /messages/).
  const drive = page.getByRole("button", { name: "HH Drive", exact: true });
  await expect(drive).toBeVisible();
  await expect(drive).toContainText("Open the HH Drive");
  await expect(page.getByRole("button", { name: "House Messages", exact: true })).toHaveAttribute("data-href", "/messages/");
  await expect(page.getByTestId("header-word")).toHaveText("Driver");
  expect(await storedFlow(page)).toBe("driver");
  // The HH Drive tile carries the box's /drive/ prefix, the same one the
  // deploy image serves HH Drive under (deploy/demo/Dockerfile line 62).
  // The stub harness here serves the front door alone; the tile's href is
  // what proves the navigation target, without taking the user to a 404.
  await expect(drive).toHaveAttribute("data-href", "/drive/");
});

test("the Payables and Receivables and Procurement manager tiles carry their one line", async ({ page }) => {
  await signIn(page, "Payables and Receivables");
  await expect(page.getByRole("button", { name: "Money Desk", exact: true })).toContainText(
    "Receivables, payables and the bounced payment queue.",
  );
  await page.getByRole("button", { name: "Switch role" }).click();
  await signIn(page, "Procurement manager");
  await expect(page.getByRole("button", { name: "Buying and Stock", exact: true })).toContainText(
    "Draft and place orders, and see what is on the way.",
  );
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
  for (const name of ["Manager Home", "Dealer Admin"]) {
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
  }
  // House Messages is Sales' own tile too (the matrix gives it at work level).
  await expect(page.getByRole("button", { name: "House Messages", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});

test("a refused mint speaks in its own card, verbatim, and both cards keep working", async ({ page }) => {
  await stubArmsRefusal(page, "sales-manager");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toContainText(
    "Sign-in did not go through. too many sign-ins just now, wait a moment and tap again",
  );
  expect(await storedKeys(page)).toEqual([]);
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  // The card that refused stays tappable, and the other card never stopped working.
  await expect(manager).toBeEnabled();
  await signIn(page, "Sales");
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});

test("the manager's five tiles all open an app of the box, so none leads to the coming-soon page", async ({ page }) => {
  await signIn(page, "Sales manager");
  for (const [name, href] of [
    ["Counter Till", "/till/"],
    ["Quote Desk", "/quote/"],
    ["Manager Home", "/home/"],
    ["Dealer Admin", "/dealer-admin/"],
    ["House Messages", "/messages/"],
  ] as const) {
    await expect(page.getByRole("button", { name, exact: true })).toHaveAttribute("data-href", href);
  }
  await expect(page.getByText("not on this demo box yet")).toHaveCount(0);
});

test("a reload in the same tab resumes the role home from the stored session", async ({ page }) => {
  await signIn(page, "Sales");
  await page.reload();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
});

// The contractor entry: the flows listing carries each row's class, and a tenant row is a contractor
// persona routed to the Pro app for its role. The card does not mint; it opens the app with the role.
const CONTRACTOR_CARDS: ReadonlyArray<[label: string, app: string, path: string]> = [
  ["Owner", "Pro Procurement", "/pro-procurement/?as=account_admin"],
  ["Buyer", "Pro Procurement", "/pro-procurement/?as=buyer"],
  ["Project manager", "Pro Procurement", "/pro-procurement/?as=buyer_no_pay"],
  ["Accounts payable", "Pro Pay", "/pro-pay/?as=ap_finance"],
  ["Field crew", "Pro Field", "/pro-field/?as=field_crew"],
];

test("the contractor accounts section lists the five roles, each naming the Pro app it opens", async ({ page }) => {
  const section = page.getByTestId("contractor-section");
  await expect(section).toBeVisible();
  await expect(section).toContainText("Summit Ridge Builders buys from Gable Landscape Supply.");
  await expect(page.getByTestId("contractor-card")).toHaveCount(5);
  for (const [label, app] of CONTRACTOR_CARDS) {
    const card = section.getByRole("button", { name: label, exact: true });
    await expect(card).toBeEnabled();
    await expect(card).toContainText(app);
    await expect(card).toContainText(`Open ${app} as ${label}`);
  }
  // The staff cards are untouched.
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
});

for (const [label, , path] of CONTRACTOR_CARDS) {
  test(`the ${label} card opens its Pro app with the role, and mints nothing at the front door`, async ({ page }) => {
    // The harness serves the front door alone, so the navigation target is answered by a stand-in page.
    await page.route("**/pro-*/**", (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<title>Pro</title>" }));
    await page.getByTestId("contractor-section").getByRole("button", { name: label, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${path.replace(/[?]/g, "\\?")}$`));
    expect(await storedKeys(page)).toEqual([]);
  });
}

test("a flows listing that fails shows no contractor cards and leaves the staff cards working", async ({ page }) => {
  expect((await page.request.post("/_stub/listing-fails")).ok()).toBe(true);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("contractor-section")).toHaveCount(0);
});
