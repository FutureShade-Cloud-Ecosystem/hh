// PR 121 round 2 fixes, the settings: a scope or branch switch never leaves another row's values enabled under the
// new label, a save that answers after the switch fills nothing, the refusal names what differs (scope or branch),
// and a refusal locks the one branch row it was for. Answers are held behind gates the tests open themselves.
import { expect, test, type Page } from "@playwright/test";
import { gate, settle } from "./switch-helpers.js";

const settings = (page: Page) => page.locator("qd-quote-settings");

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openScreenSettings(page: Page): Promise<void> {
  // The disclosure lives on the quote page under the split-list navigation:
  // open a quote first, then the detail disclosure.
  await page.getByTestId("qb-list-row").first().click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
}

const surface = (page: Page) => page.locator("qd-surface-settings");

async function setBranch(page: Page, branchId: string): Promise<void> {
  await surface(page).evaluate((el, id) => {
    (el as unknown as { branchId: string }).branchId = id;
  }, branchId);
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a failed settings read after a scope switch shows no form from the scope before", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
  await expect(page.getByTestId("qs-validity-days")).toBeVisible();
  await page.route("**/api/v1/quotes/policy**", async (route) => {
    if (route.request().method() === "GET" && !new URL(route.request().url()).searchParams.has("branch_id")) {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the policy store is down" }) });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qs-scope").selectOption("dealer");
  await expect(page.getByTestId("qs-policy-unread")).toBeVisible();
  await expect(page.getByTestId("qs-validity-days")).toHaveCount(0);
  await expect(page.getByTestId("qs-policy-save")).toHaveCount(0);
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("a save that answers after the scope switch fills nothing and says nothing under the other scope", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const held = gate();
  await page.route("**/api/v1/quotes/policy**", async (route) => {
    if (route.request().method() === "PUT") await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.method() === "PUT" && r.url().includes("/quotes/policy"));
  await page.getByTestId("qs-policy-save").click();
  await asked;
  await page.getByTestId("qs-scope").selectOption("dealer");
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
  const answered = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().includes("/quotes/policy"));
  held.open();
  await answered;
  await settle(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
  await expect(page.getByTestId("qs-notice")).toHaveCount(0);
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("a form loaded for another scope is refused in words that say scope, and one for another branch says branch", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  await expect(page.getByTestId("qs-validity-days")).toBeVisible();
  await surface(page).evaluate((el) => {
    (el as unknown as { formKey: string }).formKey = "dealer";
  });
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-error")).toContainText("another scope");
  await expect(page.getByTestId("qs-error")).not.toContainText("another branch");
  // The form is read again after a refusal; wait for it before setting up the next one.
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await surface(page).evaluate((el) => {
    (el as unknown as { formKey: string }).formKey = "branch:somewhere-else";
  });
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-error")).toContainText("another branch");
});

test("a refusal locks the one branch row it was for, not the caller's own branch", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const own = await surface(page).evaluate((el) => (el as unknown as { branchId: string }).branchId);
  await setBranch(page, "another-branch");
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-policy-lock")).toContainText("Only a sales manager of that branch changes its quote settings");
  await expect(page.getByTestId("qs-policy-save")).toBeDisabled();
  await setBranch(page, own);
  await expect(page.getByTestId("qs-policy-lock")).toHaveCount(0);
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await setBranch(page, "another-branch");
  await expect(page.getByTestId("qs-policy-save")).toBeDisabled();
});
