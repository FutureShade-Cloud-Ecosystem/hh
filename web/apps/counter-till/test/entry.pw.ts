// The Counter Till's exit test, run against the stubbed harness (the stub
// serves the demo routes' contract shapes; the vite dev server proxies /api
// to it). This is W6's slice of the plan's screen 1: two cards, a tapped card
// lands on the till with the Demo banner and the role's name, the eleven
// other roles show as coming and cannot be tapped, sign out returns to the
// cards, and against a flows route that answers there are no cards at all.
import { expect, test } from "@playwright/test";

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

test("the entry page shows the two demo role cards and the eleven coming roles", async ({ page }) => {
  await page.goto("/");
  const sales = page.getByRole("button", { name: "Sales", exact: true });
  await expect(sales).toBeVisible();
  await expect(sales).toBeEnabled();
  const manager = page.getByRole("button", { name: "Sales manager", exact: true });
  await expect(manager).toBeVisible();
  await expect(manager).toBeEnabled();

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

test("tapping Sales lands on the till with the Demo banner and Sales", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
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

test("against a flows route that answers 404 no card renders and the page says so", async ({ page }) => {
  await page.route("**/api/v1/demo/flows", (route) =>
    route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "not found" }) }),
  );
  await page.goto("/");
  await expect(
    page.getByText("This deployment does not serve the demo sign-in, so there are no role cards to tap."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toHaveCount(0);
});
