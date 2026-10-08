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
import { row, showList } from "./switch-helpers.js";

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

const settings = (page: Page) => page.locator("qd-quote-settings");

/** Open the detail disclosure and the screen settings the way a person does. */
async function openScreenSettings(page: Page): Promise<void> {
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  // round 14 (a): wait for the grant to load before interacting with the disclosure.
  await expect(settings(page).getByTestId("qs-screen")).toBeVisible();
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
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByTestId("qb-settings-btn").click();
  await expect(page.getByTestId("qb-detail")).toHaveAttribute("open", "");
  // round 14 (a): the grant gates the disclosures; wait for it to load before asserting on qs-policy.
  await expect(settings(page).getByTestId("qs-screen")).toBeVisible();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
});

test("the dealer's quantity notice is shown, held to its 500 characters, edited and read back", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const notice = page.getByTestId("qs-quantity-notice");
  await expect(notice).toHaveValue(/All quantities are to be confirmed by the customer/);
  await notice.fill("x".repeat(501));
  await expect(page.getByTestId("qs-quantity-notice-count")).toContainText("501 of 500");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-error")).toContainText("limited to 500 characters");
  expect((await ledger(page)).policyWrites).toHaveLength(0);
  await notice.fill("Quantities are yours to confirm.");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  await page.reload();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await openScreenSettings(page);
  await expect(page.getByTestId("qs-quantity-notice")).toHaveValue("Quantities are yours to confirm.");
  await page.request.post("/_stub/reset");
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
  await expect(page.getByTestId("qs-policy-lock")).toContainText("Only the general manager changes the dealer default quote settings");
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
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  // round 14 (a): wait for the grant to load before interacting with the disclosure.
  await expect(settings(page).getByTestId("qa-details")).toBeVisible();
  await settings(page).getByTestId("qa-details").locator("summary").click();
  await expect(page.getByTestId("qa-switch")).toBeVisible();
  await expect(page.getByTestId("qa-switch")).not.toBeChecked();
  // A click, not check(): the server refuses, so the box settles back where it was, which can beat the check's own re-read.
  await page.getByTestId("qa-switch").click();
  await expect(page.getByTestId("qa-lock")).toContainText("Only the general manager changes the dealer default quote settings");
  await expect(page.getByTestId("qa-switch")).toBeDisabled();
  await expect(page.getByTestId("qa-switch")).not.toBeChecked();
});

test("where the server allows it, the one switch flips and says what it now means", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  // round 14 (a): wait for the grant to load before interacting with the disclosure.
  await expect(settings(page).getByTestId("qa-details")).toBeVisible();
  await settings(page).getByTestId("qa-details").locator("summary").click();
  await page.getByTestId("qa-switch").check();
  await expect(page.getByTestId("qa-notice")).toContainText("Multiple acceptance is on");
  await page.getByTestId("qa-switch").uncheck();
  await expect(page.getByTestId("qa-notice")).toContainText("Multiple acceptance is off");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("the scope rule table lists every rule, switches one off and keeps it listed", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  // round 14 (a): General Manager has canReadScopeRules=true, so qr-details mounts.
  await expect(settings(page).getByTestId("qr-details")).toBeVisible();
  await settings(page).getByTestId("qr-details").locator("summary").click();
  await expect(page.getByTestId("qr-row")).toHaveCount(7);
  await expect(page.getByTestId("qr-summary")).toContainText("7 of 7 rules are on.");
  const ruleRow = page.getByTestId("qr-row").filter({ hasText: "WALL_NEEDS_BASE" });
  await expect(ruleRow).toContainText("WALL");
  await expect(ruleRow).toContainText("BASE");
  await ruleRow.getByRole("button").click();
  await expect(ruleRow).toHaveAttribute("data-enabled", "false");
  await expect(ruleRow.getByRole("button")).toHaveText("Off");
  await expect(page.getByTestId("qr-row")).toHaveCount(7);
  await expect(page.getByTestId("qr-summary")).toContainText("6 of 7 rules are on.");
  await expect(page.getByText("A switched-off rule stays listed")).toBeVisible();
  // There is no way to edit the wording, categories or order from this table.
  await expect(page.getByTestId("qr-table").locator("input")).toHaveCount(0);
  await ruleRow.getByRole("button").click();
  await expect(page.getByTestId("qr-summary")).toContainText("7 of 7 rules are on.");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

// round 14 (a): the grant gates the acceptance and scope rule disclosures. Sales (INSIDE_SALES) holds
// neither: they never mount, so neither admin route is read, no refused sentence appears, and no role code
// or tech admin words are on the page. The screen settings stay for every role: the policy reads (the core
// answers it with a null quick_chip_set for an unauthored chip set, as the testers' copy did) and its write
// is refused in plain words naming who may change it.
test("a caller outside the settings roles reads the policy, sees no admin sections and fires no admin reads", async ({ page }) => {
  await page.request.put("/_stub/chip-set-null", { data: { on: true } });
  const requests: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    requests.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    await route.continue();
  });

  await signIn(page, "Sales");
  // The work area offers no Screen settings entry to this caller.
  await expect(page.getByTestId("qb-settings-btn")).toHaveCount(0);
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await settings(page).getByTestId("qs-screen").locator("summary").click();
  // The policy reads for this role: the null chip set is the empty set, not an unreadable policy.
  await expect(page.getByTestId("qs-policy")).toBeVisible();
  await expect(page.getByTestId("qs-policy-unread")).toHaveCount(0);

  // The two admin sections are not there, and nothing stands in their place.
  await expect(settings(page).getByTestId("qa-details")).toHaveCount(0);
  await expect(settings(page).getByTestId("qr-details")).toHaveCount(0);
  await expect(page.getByTestId("qa-refused")).toHaveCount(0);
  await expect(page.getByTestId("qr-refused")).toHaveCount(0);
  const pageText = await page.textContent("body");
  expect(pageText).not.toMatch(/INSIDE_SALES|tech admin|Forbidden/i);

  // The grant was asked once; neither admin route was read.
  expect(requests.filter((r) => r === "GET /api/v1/quotes/settings-grant")).toHaveLength(1);
  expect(requests.filter((r) => r.includes("/quotes/settings/acceptance") || r.includes("/quote-assist/scope-rules"))).toHaveLength(0);
  expect(requests.filter((r) => r === "GET /api/v1/quotes/policy").length).toBeGreaterThan(0);

  // The policy write is refused, and the screen locks with the server's plain sentence.
  const before = (await ledger(page)).policyWrites.length;
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-policy-lock")).toContainText("Only a sales manager or the general manager changes the quote settings");
  await expect(page.getByTestId("qs-policy-save")).toBeDisabled();
  expect((await ledger(page)).policyWrites).toHaveLength(before);
  // The curation write is refused the same way.
  await page.getByTestId("qs-items-save").click();
  await expect(page.getByTestId("qs-items-lock")).toContainText("Only an administrator saves the dealer's screen layout");
  await expect(page.getByTestId("qs-items-save")).toBeDisabled();
  await page.request.put("/_stub/chip-set-null", { data: { on: false } });
});

// round 14 (a): the grant allows the Sales manager to see acceptance and screen settings
// but not scope rules; the reads fire and succeed.
test("a Sales manager sees acceptance and screen settings but not scope rules, and the reads fire", async ({ page }) => {
  const requests: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    requests.push(`${route.request().method()} ${route.request().url()}`);
    await route.continue();
  });

  await signIn(page, "Sales manager");
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();

  // Acceptance and screen settings disclosures mount; scope rules does not.
  await expect(settings(page).getByTestId("qa-details")).toBeVisible();
  await expect(settings(page).getByTestId("qs-screen")).toBeVisible();
  await expect(settings(page).getByTestId("qr-details")).toHaveCount(0);

  // Both allowed reads fire.
  const scopeRuleReads = requests.filter((r) => r.includes("/quote-assist/scope-rules"));
  await expect.poll(() => requests.filter((r) => r.includes("/quotes/settings/acceptance")).length).toBeGreaterThan(0);
  expect(scopeRuleReads).toHaveLength(0);
});

// round 14 (a): the General Manager sees all three disclosures and all three reads fire.
test("the General Manager sees all three disclosures and all three reads fire", async ({ page }) => {
  const requests: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    requests.push(`${route.request().method()} ${route.request().url()}`);
    await route.continue();
  });

  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();

  // All three disclosures mount.
  await expect(settings(page).getByTestId("qa-details")).toBeVisible();
  await expect(settings(page).getByTestId("qs-screen")).toBeVisible();
  await expect(settings(page).getByTestId("qr-details")).toBeVisible();

  // All three reads fire.
  const scopeRuleReads = requests.filter((r) => r.includes("/quote-assist/scope-rules"));
  await expect.poll(() => requests.filter((r) => r.includes("/quotes/settings/acceptance")).length).toBeGreaterThan(0);
  await expect.poll(() => requests.filter((r) => r.includes("/quote-assist/scope-rules")).length).toBeGreaterThan(0);
  await expect.poll(() => requests.filter((r) => r.includes("/quotes/policy")).length).toBeGreaterThan(0);

  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

// round 14 (a): the grant belongs to the person. One page, the General Manager out and a salesperson in: the
// salesperson is not shown the General Manager's sections and no admin route is read for them.
test("the grant does not outlive the person: a salesperson who signs in after the General Manager gets no admin sections", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await expect(settings(page).getByTestId("qr-details")).toBeVisible();
  await expect(settings(page).getByTestId("qa-details")).toBeVisible();

  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page.request.put("/_stub/general-manager", { data: { member: false } });
  const after: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    after.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    await route.continue();
  });
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await expect(settings(page).getByTestId("qs-screen")).toBeVisible();
  await expect(settings(page).getByTestId("qa-details")).toHaveCount(0);
  await expect(settings(page).getByTestId("qr-details")).toHaveCount(0);
  expect(after.filter((r) => r.includes("/quotes/settings/acceptance") || r.includes("/quote-assist/scope-rules"))).toHaveLength(0);
  expect(after.filter((r) => r === "GET /api/v1/quotes/settings-grant")).toHaveLength(1);
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
  await expect(page.getByTestId("qs-policy-lock")).toContainText("Only the general manager changes the dealer default quote settings");
  await page.getByTestId("qs-scope").selectOption("branch");
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await page.locator("qd-surface-settings").evaluate((el) => {
    (el as unknown as { branchId: string }).branchId = "another-branch";
  });
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
});
