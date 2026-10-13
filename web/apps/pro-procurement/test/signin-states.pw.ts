// The sign in screen in a real browser, in the board's states (HH: Pro, shell-signin-phone-brand and
// build_signin_states.py): the demo set (email link and Google), every method, one method only, the
// refused method, and the demo role picker. The stub adapter reads the state from the page address
// (?signin-methods, ?signin-refused, ?signin-personas), a development double only.
import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";

const SHOTS = process.env.PRO_SHOTS ?? "";

test.use({ viewport: { width: 390, height: 844 } });

async function shot(page: import("@playwright/test").Page, name: string): Promise<void> {
  if (SHOTS === "") return;
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });
}

test.beforeEach(async ({ request }) => {
  expect((await request.post("/_stub/reset")).ok()).toBe(true);
});

test("the demo set: email link first and primary, Google beneath an or rule, no password field", async ({ page }) => {
  await page.goto("/?signin-methods=email_link,google");
  await expect(page.getByRole("button", { name: "Email me a sign in link" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue with Google" })).toBeVisible();
  await expect(page.getByTestId("signin-password")).toHaveCount(0);
  await expect(page.getByTestId("signin-hint")).toHaveText("Use the email your contractor account knows.");
  await expect(page.getByText("Google works where your Google email matches your contractor account email.")).toBeVisible();
  await shot(page, "signin-demo");
});

test("every method lists all four and says the dealer chooses", async ({ page }) => {
  await page.goto("/?signin-methods=google,microsoft,futurebuild,email_link");
  for (const name of ["Email me a sign in link", "Continue with Google", "Continue with Microsoft"]) await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
  await expect(page.getByTestId("signin-password")).toBeVisible();
  await expect(page.getByTestId("signin-hint")).toContainText("chooses the methods its contractors use");
  await shot(page, "signin-every");
});

test("one method only shows no alternatives", async ({ page }) => {
  await page.goto("/?signin-methods=email_link");
  await expect(page.getByRole("button")).toHaveCount(1);
  await expect(page.getByTestId("signin-hint")).toHaveText("This dealer allows one method.");
  await shot(page, "signin-one");
});

test("a refused method names the dealer and the methods it does allow", async ({ page }) => {
  await page.goto("/?signin-methods=email_link,google&signin-refused=microsoft");
  await expect(page.getByTestId("signin-refusal")).toHaveText(/does not allow sign in with Microsoft\. Use your email link or Google\.$/);
  await expect(page.getByRole("button", { name: "Continue with Microsoft" })).toHaveCount(0);
  await shot(page, "signin-refused");
});

test("no word on the card names the machinery", async ({ page }) => {
  await page.goto("/?signin-methods=google,microsoft,futurebuild,email_link&signin-refused=microsoft&signin-personas=1");
  const text = (await page.locator("hh-pro-signin").innerText()).toLowerCase();
  for (const word of ["console", "core", "token", "session"]) expect(text).not.toContain(word);
});

test("a demo role picked from the card signs that role in, and the server's role answers", async ({ page }) => {
  await page.goto("/?signin-methods=email_link,google&signin-personas=1");
  await expect(page.getByTestId("signin-demo-roles")).toBeVisible();
  await shot(page, "signin-demo-roles");
  await page.getByRole("button", { name: "Project manager", exact: true }).click();
  await expect(page.locator("hh-pro-frame")).toBeVisible();
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-role", "Project manager");
});

test("an email that is not a demo account is refused in plain words and nothing signs in", async ({ page }) => {
  await page.goto("/?signin-methods=email_link&signin-personas=1");
  await page.getByTestId("signin-email").fill("someone@example.com");
  await page.getByRole("button", { name: "Email me a sign in link" }).click();
  await expect(page.getByTestId("signin-message")).toContainText(/not open here|Pick a role/);
  await expect(page.locator("hh-pro-frame")).toHaveCount(0);
});
