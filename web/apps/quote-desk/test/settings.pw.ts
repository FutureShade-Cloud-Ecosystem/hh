// The three settings screens in a real browser, against the stubbed harness:
// the quote policy (a branch row and the dealer wide default, with field
// validation), the per role curation of columns, panels and actions, the one
// acceptance switch, and the scope rule table. The role rules are the
// server's alone: the app offers both scopes and the switch and shows what
// the server answers. The Sales manager (the stub's manager login) writes a
// branch row; the dealer wide default and the acceptance switch are the
// General Manager's, which the stub grants only while its switch is on. A
// refused write locks its control with the server's own sentence, and an
// off-list caller gets the server's sentence in place of the screen's
// controls. The stub's world is put back at both ends of the file.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

const settings = (page: Page) => page.locator("qd-quote-settings");

/** Open the detail disclosure and the screen settings the way a person does. */
async function openScreenSettings(page: Page): Promise<void> {
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
}

async function ledger(page: Page): Promise<{
  policyWrites: Array<{ scope: string; default_validity_days?: number }>;
  itemsWrites: Array<{ body: { profile: string; count: number } }>;
}> {
  return (await (await page.request.get("/_stub/state")).json()) as {
    policyWrites: Array<{ scope: string; default_validity_days?: number }>;
    itemsWrites: Array<{ body: { profile: string; count: number } }>;
  };
}

test.beforeAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the work area's Screen settings button opens the settings region", async ({ page }) => {
  await signIn(page);
  // The button sits on the open quote's panel; a quote has to be open.
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByTestId("qb-settings-btn").click();
  await expect(page.getByTestId("qb-detail")).toHaveAttribute("open", "");
  await expect(page.getByTestId("qs-policy")).toBeVisible();
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
});

test("the quote policy saves a branch row, validated first, and reads back", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  // A value the server would refuse is refused here first, naming the knob, and nothing is sent.
  await page.getByTestId("qs-validity-days").fill("0");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-error")).toContainText("Default validity must be a whole number of days from 1 to 365");
  expect((await ledger(page)).policyWrites).toHaveLength(0);
  await page.getByTestId("qs-validity-days").fill("45");
  await page.getByTestId("qs-send-sms").uncheck();
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  const writes = (await ledger(page)).policyWrites;
  expect(writes).toHaveLength(1);
  expect(writes[0]?.scope).toBe("branch");
  // Back in on a fresh load: the branch row is what the screen reads.
  await page.reload();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await openScreenSettings(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("45");
  await expect(page.getByTestId("qs-send-sms")).not.toBeChecked();
});

/** The fan-out the builder runs after any edit elsewhere on the page, and the policy read it brings. */
async function fanOut(page: Page): Promise<void> {
  const read = page.waitForResponse((r) => r.url().includes("/api/v1/quotes/policy") && r.request().method() === "GET");
  await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    root?.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "" } }));
  });
  await read;
  await page.waitForTimeout(300);
}

test("a re-read never undoes an unsaved edit on the policy or the layout", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const written = (await ledger(page)).policyWrites.length;
  await page.getByTestId("qs-validity-days").fill("77");
  await page.getByTestId("qs-send-email").uncheck();
  await page.getByTestId("qs-items").getByLabel("Show Photo").uncheck();
  await fanOut(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("77");
  await expect(page.getByTestId("qs-send-email")).not.toBeChecked();
  await expect(page.getByTestId("qs-items").getByLabel("Show Photo")).not.toBeChecked();
  // Nothing was written by any of it.
  expect((await ledger(page)).policyWrites).toHaveLength(written);
  // Switching scope starts that scope clean: the unsaved figure belongs to the scope it was typed in.
  await page.getByTestId("qs-scope").selectOption("dealer");
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
});

test("quick chips are picked in click order and removed by a second click", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const chips = page.getByTestId("qs-chips");
  // The saved chips lead; a category from the catalogue's own vocabulary is added after them.
  await expect(chips.getByRole("button", { name: /1 · Pavers/ })).toBeVisible();
  await chips.getByRole("button", { name: "Edging", exact: true }).click();
  await expect(chips.getByRole("button", { name: /4 · Edging/ })).toBeVisible();
  await chips.getByRole("button", { name: /1 · Pavers/ }).click();
  await expect(chips.getByRole("button", { name: /3 · Edging/ })).toBeVisible();
});

test("the dealer wide default is refused by the server for this role: the scope locks with the server's sentence", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  await page.getByTestId("qs-scope").selectOption("dealer");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-policy-lock")).toContainText("The dealer wide default is set by the General Manager");
  await expect(page.getByTestId("qs-policy-save")).toBeDisabled();
  await expect(page.getByTestId("qs-validity-days")).toBeDisabled();
  await expect(page.getByTestId("qs-cost-visibility")).toBeDisabled();
  // The refusal belongs to that scope only: the branch row is still writable.
  await page.getByTestId("qs-scope").selectOption("branch");
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await expect(page.getByTestId("qs-policy-lock")).toHaveCount(0);
});

test("where the server allows the dealer wide default, the same screen saves it", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
  await page.getByTestId("qs-scope").selectOption("dealer");
  await page.getByTestId("qs-validity-days").fill("60");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  const writes = (await ledger(page)).policyWrites;
  expect(writes[writes.length - 1]?.scope).toBe("dealer");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("the layout curation lists the registry, marks cost columns server gated, and saves per profile", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const items = page.getByTestId("qs-items");
  await expect(items.getByRole("table", { name: "Panels" })).toBeVisible();
  await expect(items.getByRole("table", { name: "Line table columns" })).toBeVisible();
  await expect(items.getByRole("table", { name: "Actions" })).toBeVisible();
  // The minimum panels are always on and cannot be switched off.
  await expect(items.getByLabel("Show Products")).toBeDisabled();
  await expect(items.getByLabel("Show Products")).toBeChecked();
  // Cost columns are marked and off by default.
  await expect(items.getByLabel("Show Cost", { exact: true })).not.toBeChecked();
  await expect(items.getByRole("row", { name: /Cost/ }).first()).toContainText("cost: server gated");
  // Switch a column off, move another up, save for the counter profile.
  await items.getByLabel("Show Photo").uncheck();
  await items.getByRole("table", { name: "Line table columns" }).getByRole("button", { name: "Move up" }).nth(2).click();
  await items.getByTestId("qs-items-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Screen layout saved for the counter profile");
  const writes = (await ledger(page)).itemsWrites;
  expect(writes[writes.length - 1]?.body.profile).toBe("counter");
  // The saved rows are what the profile reads back.
  await page.reload();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await openScreenSettings(page);
  await expect(page.getByTestId("qs-items").getByLabel("Show Photo")).not.toBeChecked();
  // Another profile starts from the catalogue, not from the counter's rows.
  await page.getByTestId("qs-profile").selectOption("yard");
  await expect(page.getByTestId("qs-items").getByLabel("Show Photo")).toBeChecked();
});

test("the acceptance switch reads for this role, and the server refuses the dealer wide write: the switch locks with its sentence", async ({ page }) => {
  await signIn(page);
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qa-details").locator("summary").click();
  await expect(page.getByTestId("qa-switch")).toBeVisible();
  await expect(page.getByTestId("qa-switch")).not.toBeChecked();
  // A click, not check(): the server refuses, so the box settles back where it was, which can beat the check's own re-read.
  await page.getByTestId("qa-switch").click();
  await expect(page.getByTestId("qa-lock")).toContainText("only the General Manager changes it");
  await expect(page.getByTestId("qa-switch")).toBeDisabled();
  await expect(page.getByTestId("qa-switch")).not.toBeChecked();
});

test("where the server allows it, the one switch flips and says what it now means", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qa-details").locator("summary").click();
  await page.getByTestId("qa-switch").check();
  await expect(page.getByTestId("qa-notice")).toContainText("Multiple acceptance is on");
  await page.getByTestId("qa-switch").uncheck();
  await expect(page.getByTestId("qa-notice")).toContainText("Multiple acceptance is off");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("the scope rule table lists every rule, switches one off and keeps it listed", async ({ page }) => {
  await signIn(page);
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qr-details").locator("summary").click();
  await expect(page.getByTestId("qr-row")).toHaveCount(7);
  await expect(page.getByTestId("qr-summary")).toContainText("7 of 7 rules are on.");
  const row = page.getByTestId("qr-row").filter({ hasText: "WALL_NEEDS_BASE" });
  await expect(row).toContainText("WALL");
  await expect(row).toContainText("BASE");
  await row.getByRole("button").click();
  await expect(row).toHaveAttribute("data-enabled", "false");
  await expect(row.getByRole("button")).toHaveText("Off");
  await expect(page.getByTestId("qr-row")).toHaveCount(7);
  await expect(page.getByTestId("qr-summary")).toContainText("6 of 7 rules are on.");
  await expect(page.getByText("A switched-off rule stays listed")).toBeVisible();
  // There is no way to edit the wording, categories or order from this table.
  await expect(page.getByTestId("qr-table").locator("input")).toHaveCount(0);
  await row.getByRole("button").click();
  await expect(page.getByTestId("qr-summary")).toContainText("7 of 7 rules are on.");
});

test("a caller outside the settings roles sees the server's sentences in place of the controls", async ({ page }) => {
  await signIn(page, "Sales");
  // The work area offers no Screen settings entry to this caller.
  await expect(page.getByTestId("qb-settings-btn")).toHaveCount(0);
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qs-screen").locator("summary").click();
  await settings(page).getByTestId("qa-details").locator("summary").click();
  await settings(page).getByTestId("qr-details").locator("summary").click();
  // The acceptance switch and the scope rules: the server refused the read, so its sentence stands.
  await expect(page.getByTestId("qa-refused")).toContainText("may not read them");
  await expect(page.getByTestId("qa-switch")).toHaveCount(0);
  await expect(page.getByTestId("qr-refused")).toContainText("Only an administrator reads the scope rules");
  await expect(page.getByTestId("qr-row")).toHaveCount(0);
  // The policy reads; its write is refused, and the screen locks with the server's sentence.
  await expect(page.getByTestId("qs-policy")).toBeVisible();
  const before = (await ledger(page)).policyWrites.length;
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-policy-lock")).toContainText("The Inside Sales role may not change the quote policy");
  await expect(page.getByTestId("qs-policy-save")).toBeDisabled();
  expect((await ledger(page)).policyWrites).toHaveLength(before);
  // The curation write is refused the same way.
  await page.getByTestId("qs-items-save").click();
  await expect(page.getByTestId("qs-items-lock")).toContainText("Only an administrator saves the dealer's screen layout");
  await expect(page.getByTestId("qs-items-save")).toBeDisabled();
});

// ── PR 121 round 1: a switch never wipes or misdirects what the person typed ──

test("a value typed right after the scope switch is not wiped by the slow read, and the save carries it", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
  await page.route("**/api/v1/quotes/policy**", async (route) => {
    if (route.request().method() === "GET") await new Promise((resolve) => setTimeout(resolve, 700));
    await route.continue();
  });
  await page.getByTestId("qs-scope").selectOption("dealer");
  // The form is held (disabled) until the read lands, so this fill waits for it and is then the last word.
  await page.getByTestId("qs-validity-days").fill("77");
  await page.waitForTimeout(900);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("77");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  const writes = (await ledger(page)).policyWrites;
  expect(writes[writes.length - 1]).toMatchObject({ scope: "dealer", default_validity_days: 77 });
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("a dirty branch form is never saved to the branch that is open now", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const before = (await ledger(page)).policyWrites.length;
  await page.locator("qd-surface-settings").evaluate((el) => {
    (el as unknown as { branchId: string }).branchId = "another-branch";
  });
  await expect(page.getByTestId("qs-notice")).toContainText("The branch changed");
  await expect(page.getByTestId("qs-validity-days")).not.toHaveValue("55");
  await page.getByTestId("qs-policy-save").click();
  const writes = (await ledger(page)).policyWrites;
  for (const write of writes.slice(before)) expect(write).not.toMatchObject({ default_validity_days: 55 });
});

test("a refusal on one branch does not lock another branch's row", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  await page.getByTestId("qs-scope").selectOption("dealer");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-policy-lock")).toContainText("The dealer wide default is set by the General Manager");
  await page.getByTestId("qs-scope").selectOption("branch");
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await page.locator("qd-surface-settings").evaluate((el) => {
    (el as unknown as { branchId: string }).branchId = "another-branch";
  });
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
});
