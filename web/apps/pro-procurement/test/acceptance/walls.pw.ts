// The Pro Procurement acceptance leg that carries the wall: a role without the
// capability is refused, at the screen and at the server. It has its own
// config (acceptance.config.ts) and its own stub and app pair in the Pro
// cycle's range, the same shape as Buying and Stock's acceptance set. The
// L-branch of the Buying and Stock set (a second branch's role seeing only its
// own branch) has no tenant counterpart: the tenant routes carry no branch
// switch, so there is nothing here for it to hold.
import { test, expect, type Page } from "@playwright/test";

const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;

async function signIn(page: Page, who: string): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator("hh-pro-frame")).toBeVisible();
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.use({ viewport: { width: 390, height: 844 } });

test("L wall: a field crew role is refused the landing with no call, and the procurement surfaces are not offered", async ({ page }) => {
  const calls: string[] = [];
  page.on("request", (r) => calls.push(new URL(r.url()).pathname));
  await signIn(page, "field");
  const state = page.getByTestId("surface-state");
  await expect(state).toHaveAttribute("data-state", "refusal");
  await expect(state).toContainText("Your role does not open this");
  expect(calls).not.toContain("/api/v1/tenant/dashboard");
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await expect(panel).toBeVisible();
  for (const surface of ["Board", "Catalog", "Quote studio", "Projects"]) {
    await expect(panel.getByRole("button", { name: surface, exact: true })).toHaveCount(0);
  }
  // What the role does hold is still offered (Pro Field's tracking; another
  // app's entry reads with its note, so the name is matched loosely).
  await expect(panel.getByRole("button", { name: /Tracking/ })).toBeVisible();
});

test("L wall: accounts payable reads the account, is not offered the buying surfaces, and the kit catalogue's Projects entry still ends at the wall", async ({ page }) => {
  const calls: string[] = [];
  page.on("request", (r) => calls.push(new URL(r.url()).pathname));
  await signIn(page, "ap");
  await expect(page.getByTestId("today-rows")).toBeVisible();
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  for (const surface of ["Board", "Catalog", "Quote studio"]) {
    await expect(panel.getByRole("button", { name: surface, exact: true })).toHaveCount(0);
  }
  await expect(panel.getByRole("button", { name: /Invoices/ })).toBeVisible();
  // The kit's catalogue gates Projects with view_billing; the procurement
  // routes answer to submit_rfq, which accounts payable does not hold. The
  // entry is offered, the app refuses it at the gate, and no call is made:
  // the server's boundary holds even where the chrome is wrong.
  await panel.getByRole("button", { name: "Projects", exact: true }).click();
  const state = page.getByTestId("surface-state");
  await expect(state).toHaveAttribute("data-state", "refusal");
  expect(calls).not.toContain("/api/v1/tenant/procurement/projects");
});

test("L wall: the server holds the line even where the role would pass, and the refusal says so", async ({ page, request }) => {
  await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/dashboard", status: 403 } } });
  await signIn(page, "buyer");
  const state = page.getByTestId("surface-state");
  await expect(state).toHaveAttribute("data-state", "refusal");
  await expect(state).toContainText("Your role does not open this");
  await expect(state.getByRole("button", { name: "Try again" })).toHaveCount(0);
});

test("L wall: the project manager's money wall is the catalogue: no payment methods surface is offered", async ({ page }) => {
  await signIn(page, "pm");
  await expect(page.getByTestId("today-rows")).toBeVisible();
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  // The project manager builds plans and converts quotes, and cannot manage money.
  await expect(panel.getByRole("button", { name: /Payment methods/ })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Board", exact: true })).toBeVisible();
});
