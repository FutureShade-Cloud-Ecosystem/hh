// The Counter Till's exit test, run against the stubbed harness (the stub
// serves the demo routes' contract shapes; the vite dev server proxies /api
// to it). This is W6's slice of the plan's screen 1: two cards, a tapped card
// lands on the till with the Demo banner and the role's name, the eleven
// other roles show as coming and cannot be tapped, sign out returns to the
// cards, and against a flows route that answers there are no cards at all.
// The suite also holds the org header: the stub echoes the X-Org it received
// back as x-stub-saw-org, so a wrong org in the app's client configuration
// fails right here.
import { expect, test, type Page, type Response } from "@playwright/test";

// The decided matrix's labels for the roles whose screens are not in this
// release, mirroring the app's own list (src/views/entry.ts): a test that
// carried the list from the app would pass on an app that forgot a role.
const COMING_ROLES = [
  "Outside Sales",
  "Yard Team",
  "Yard Manager",
  "Logistics Manager",
  "Driver",
  "Branch Manager",
  "Procurement Manager",
  "Payables and Receivables",
  "Financial Controller",
  "General Manager",
  "HR",
];

function flowsCall(page: Page): Promise<Response> {
  return page.waitForResponse((response) => response.url().endsWith("/api/v1/demo/flows"));
}

test("the entry page shows the two demo role cards and the eleven coming roles", async ({ page }) => {
  const flows = flowsCall(page);
  await page.goto("/");
  // The demo org rides on the flows call: it is the client configuration's
  // own constant, sent through the auth package's staff header provider.
  expect((await flows).headers()["x-stub-saw-org"]).toBe("hh-demo");

  const sales = page.getByRole("button", { name: "Sales", exact: true });
  await expect(sales).toBeVisible();
  await expect(sales).toBeEnabled();
  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toBeVisible();
  await expect(manager).toBeEnabled();

  // The stub's table, like the core's, mints the full closed set of seven
  // logins (sales, sales manager, the Yard Team, the Driver, Payables and
  // Receivables, the Procurement Manager, the Financial Controller); the
  // till offers a card for its own pair alone (any other card would lead
  // into a surface the server refuses), and those roles stay in the
  // coming list.
  const listing = (await (await flows).json()) as { flows: Array<{ flow: string }> };
  expect(listing.flows.map((row) => row.flow)).toEqual([
    "sales",
    "sales-manager",
    "yard-team",
    "driver",
    "payables-receivables",
    "procurement-manager",
    "financial-controller",
  ]);
  await expect(page.getByRole("button", { name: "Yard Team", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Driver", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Payables and Receivables", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Procurement Manager", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Financial Controller", exact: true })).toBeDisabled();

  const coming = page.getByRole("list", { name: "Coming roles" });
  await expect(coming).toBeVisible();
  await expect(coming.getByRole("listitem")).toHaveCount(11);
  for (const role of COMING_ROLES) {
    const control = coming.getByRole("button", { name: role, exact: true });
    await expect(control).toBeVisible();
    await expect(control).toBeDisabled();
    await expect(coming.getByRole("listitem").filter({ hasText: role })).toContainText("Coming");
  }
});

test("the entry stage wears the till's own cards: a card names what it opens, focus rings it, and the page holds at phone width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const sales = page.getByRole("button", { name: "Sales", exact: true });
  await expect(sales).toBeVisible();
  // The card says what the role opens, beside a mark from the ported registry.
  await expect(sales).toContainText("The counter sale flow.");
  await expect(sales.locator("svg")).toHaveCount(1);
  // Focus lands the surface's focus ring on the card: keyboard Entry reaches it.
  await sales.focus();
  await expect(sales).toBeFocused();
  await expect(sales).toHaveCSS("outline-style", "solid");
  await expect(sales).toHaveCSS("outline-width", "2px");
  // The stage holds at phone width: no horizontal overflow at 390.
  const overflow = await page.evaluate(() => {
    const scroller = document.scrollingElement;
    if (scroller === null) return 0;
    return scroller.scrollWidth - scroller.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(0);
  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toContainText("The counter plus approvals and margins.");
});

test("tapping Sales lands on the till with the Demo banner and Sales", async ({ page }) => {
  await page.goto("/");
  const mint = page.waitForResponse((response) => response.url().endsWith("/api/v1/demo/session"));
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  // The mint call carries the demo org too: it is the same configuration.
  expect((await mint).headers()["x-stub-saw-org"]).toBe("hh-demo");

  const banner = page.getByRole("note");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("Demo");
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  await expect(page.getByRole("heading", { name: "Till", exact: true })).toBeVisible();
});

test("tapping Sales manager lands on the till named Sales manager", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
  await expect(page.getByRole("heading", { name: "Till", exact: true })).toBeVisible();
});

test("a coming role cannot be tapped: the control is disabled and forcing a click changes nothing", async ({ page }) => {
  await page.goto("/");
  const yard = page.getByRole("button", { name: "Yard Team", exact: true });
  await expect(yard).toBeDisabled();
  await yard.dispatchEvent("click");
  await expect(page.getByRole("heading", { name: "Till", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
});

test("sign out returns to the cards", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Till", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
});

test("against a flows route that answers 404 no card renders, the page says so, and no banner shows", async ({ page }) => {
  await page.route("**/api/v1/demo/flows", (route) =>
    route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "not found" }) }),
  );
  await page.goto("/");
  await expect(
    page.getByText("This deployment does not serve the demo sign-in, so there are no role cards to tap."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toHaveCount(0);
  await expect(page.getByRole("note")).toHaveCount(0);
});

test("a flows route failing with 500 reads as an error with the status, not as a box without the demo", async ({ page }) => {
  await page.route("**/api/v1/demo/flows", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "boom" }) }),
  );
  await page.goto("/");
  await expect(page.getByText("The demo roles could not be loaded (HTTP 500). Reload to try again.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("note")).toHaveCount(0);
});

test("a flows call that does not go through reads as an error, not as a box without the demo", async ({ page }) => {
  await page.route("**/api/v1/demo/flows", (route) => route.abort("failed"));
  await page.goto("/");
  await expect(
    page.getByText("The demo roles could not be loaded: the call did not go through. Reload to try again."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("note")).toHaveCount(0);
});

test("an empty flow list shows its own message, no cards and no banner", async ({ page }) => {
  await page.route("**/api/v1/demo/flows", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ flows: [] }) }),
  );
  await page.goto("/");
  await expect(page.getByText("The demo sign-in answered with no roles: there is nothing to tap.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByText("Tap a role to open the till:")).toHaveCount(0);
  await expect(page.getByRole("note")).toHaveCount(0);
});
