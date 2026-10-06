// The review round's cases for the pricing panel, in a real browser against the stubbed
// harness: a check that could not grade is never a silent save, a rule's end date holds
// through an edit as the core stores it, a customer and category rule keeps its customer
// and category through an edit, the tabs follow the tab pattern, the counts wait for the
// load, focus stays in the open dialog, a confirmation the log still owes is not lost
// quietly, and the log names products and keeps a BYPASS row's reason.
import { expect, test, type Locator, type Page } from "@playwright/test";
import { openPricing, openTab, pricingState, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const PAVERS = "ca700000-0000-4000-9000-000000000001";

async function openRules(page: Page): Promise<void> {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "rules");
  await expect(page.getByTestId("pr-row")).toHaveCount(5);
}

/** A new tier rule on Pavers at a depth that leaves a margin: the form filled, the Save pressed. */
async function saveNewPaversRule(page: Page, percent: string): Promise<void> {
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-category").selectOption(PAVERS);
  await page.getByTestId("pr-value").fill(percent);
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-save").click();
}

/** Whether the element that has focus (through every shadow root) sits inside the locator. */
async function focusIsIn(target: Locator): Promise<boolean> {
  return target.evaluate((root) => {
    let node: Element | null = document.activeElement;
    while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
    return node !== null && root.contains(node);
  });
}

// ── UI P2-1: never a silent save past a check that could not grade ───────────────────────────────────

test("a check that graded nothing opens a notice and a decline saves nothing", async ({ page }) => {
  await openRules(page);
  await setMode(page, { force_preview: { items: [], not_graded: 9, no_cost: 0, truncated: false } });
  await saveNewPaversRule(page, "5");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await expect(page.getByTestId("floor-headline")).toHaveText("The floor check could not cover everything this price reaches.");
  await expect(page.getByTestId("floor-note")).toHaveText("9 products could not be priced, so they could not be checked.");
  await expect(page.getByTestId("floor-item")).toHaveCount(0);
  await page.getByTestId("floor-decline").click();
  expect((await pricingState(page)).writes).toHaveLength(0);
});

test("confirming a notice that names no product saves the price, says no log row is written, and logs nothing", async ({ page }) => {
  await openRules(page);
  await setMode(page, { force_preview: { items: [], not_graded: 2, no_cost: 0, truncated: false } });
  await saveNewPaversRule(page, "5");
  await expect(page.getByTestId("floor-dialog")).toContainText("a notice that names no product writes no row to the pricing log");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  const state = await pricingState(page);
  expect(state.writes).toHaveLength(1);
  expect(state.confirmations).toHaveLength(0);
});

test("a check that stopped at its limit opens a notice that says so", async ({ page }) => {
  await openRules(page);
  await setMode(page, { force_preview: { items: [], evaluated: 200, truncated: true } });
  await saveNewPaversRule(page, "5");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await expect(page.getByTestId("floor-note")).toHaveText("The check stopped at its limit after 200 products; the rest could not be checked.");
});

test("products with no cost on file are said in the notice", async ({ page }) => {
  await openRules(page);
  await setMode(page, { force_preview: { items: [], no_cost: 3 } });
  await saveNewPaversRule(page, "5");
  await expect(page.getByTestId("floor-note")).toHaveText("3 products have no cost on file, so their margin could not be told.");
});

test("a save past products already under the floor and no worse goes through and says so afterwards", async ({ page }) => {
  await openRules(page);
  await saveNewPaversRule(page, "18");
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  await expect(page.getByTestId("floor-settled")).toContainText("already under");
  expect((await pricingState(page)).writes).toHaveLength(1);
});

test("a 422 from the check says what was refused, not that the check could not run", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_next: { kind: "preview", status: 422, message: "discount_bps must be between 0 and 10000 basis points" } });
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-form-error")).toHaveText("Discount_bps must be between 0 and 10000 basis points. Nothing was saved.");
});

// ── UI P2-2: the end date holds through an edit ──────────────────────────────────────────────────────

test("a rule's end date is the day typed after a save and after the next edit, as the core stores it", async ({ page }) => {
  await openRules(page);
  const edit = page.getByTestId("pr-row").filter({ hasText: "Contractor, default" }).getByTestId("pr-edit");
  await edit.click();
  await page.getByTestId("pr-valid-to").fill("2026-11-15");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  const rule = async () => (await pricingState(page)).rules.find((r) => r.detail_text === "Contractor, default: 15% off");
  expect((await rule())?.valid_to).toBe("2026-11-16T00:00:00Z");
  await edit.click();
  await expect(page.getByTestId("pr-valid-to")).toHaveValue("2026-11-15");
  await page.getByTestId("pr-detail").fill("Contractor, default: 15% off, reviewed");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  const again = (await pricingState(page)).rules.find((r) => r.detail_text === "Contractor, default: 15% off, reviewed");
  expect(again?.valid_to).toBe("2026-11-16T00:00:00Z");
});

// ── UI P2-3: a customer and category rule keeps what it names ────────────────────────────────────────

test("editing a customer and category rule keeps its customer and category", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-row").filter({ hasText: "Maple Ridge, Pavers" }).getByTestId("pr-edit").click();
  await expect(page.getByTestId("pr-scope")).toHaveValue("CUSTOMER_CATEGORY");
  await expect(page.getByTestId("pr-customer-id")).toHaveValue("c0000000-0000-4000-9000-000000000001");
  await expect(page.getByTestId("pr-category")).toHaveValue(PAVERS);
  await page.getByTestId("pr-value").fill("9");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  const rule = (await pricingState(page)).rules.find((r) => r.detail_text === "Maple Ridge, Pavers: 8% off");
  expect(rule).toMatchObject({ scope: "CUSTOMER_CATEGORY", customer_id: "c0000000-0000-4000-9000-000000000001", category_id: PAVERS, discount_bps: 900 });
});

// ── P3: priority, tabs, counts, focus, the owed log row ──────────────────────────────────────────────

test("priority takes digits only", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-value").fill("5");
  for (const odd of ["1e3", "0x10"]) {
    await page.getByTestId("pr-priority").fill(odd);
    await page.getByTestId("pr-save").click();
    await expect(page.getByTestId("pr-form-error")).toContainText("Priority must be a whole number, digits only.");
  }
  expect((await pricingState(page)).writes).toHaveLength(0);
});

test("the tabs are a tablist, and the arrow keys, Home and End move between them", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await expect(page.getByRole("tablist", { name: "Pricing sections" })).toBeVisible();
  await expect(page.getByRole("tab")).toHaveCount(5);
  await expect(page.getByRole("tab", { name: "Margin policy" })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "Margin policy" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Rules" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab", { name: "Rules" })).toBeFocused();
  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: "Pricing log" })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Margin policy" })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowLeft");
  await expect(page.getByRole("tab", { name: "Pricing log" })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Margin policy" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel")).toBeVisible();
});

test("a count is not shown while its list is still loading", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/pricing/rules*", async (route) => {
    await gate;
    await route.continue();
  });
  await page.getByTestId("pricing-tab-rules").click();
  await expect(page.getByText("Loading the rules...")).toBeVisible();
  await expect(page.getByTestId("pr-count")).toHaveCount(0);
  release();
  await expect(page.getByTestId("pr-count")).toHaveText("5 rules");
});

test("Do not save returns focus inside the rule dialog that is still open", async ({ page }) => {
  await openRules(page);
  await saveNewPaversRule(page, "40");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await page.getByTestId("floor-decline").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  expect(await focusIsIn(page.getByTestId("pr-dialog"))).toBe(true);
});

test("when the log write fails focus stays in the warning, on a control that works", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  expect(await focusIsIn(page.getByTestId("floor-dialog"))).toBe(true);
});

test("a saved price whose confirmation is not logged is not closed quietly", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  // Close says so first and holds; the second Close leaves.
  await page.getByTestId("floor-close").click();
  await expect(page.getByTestId("floor-error")).toContainText("its confirmation is not in the pricing log");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await page.getByTestId("floor-close").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
});

test("Escape on a warning with the log row owed holds once, then leaves", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await expect(page.getByTestId("floor-error")).toContainText("its confirmation is not in the pricing log");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
});

// The warning's backdrop covers the page, so a pointer cannot reach the tabs or the desk bar
// while it is open; these leaves are the ones a keyboard shortcut, an assistive tool or a later
// change to the backdrop could make, so the click is dispatched at the control itself.
test("a tab switch holds once while the log row is owed, then goes", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  await page.getByTestId("pricing-tab-audit").dispatchEvent("click");
  await expect(page.getByTestId("pricing-leave-warning")).toBeVisible();
  await expect(page.getByTestId("pricing-tab-rules")).toHaveAttribute("aria-selected", "true");
  await page.getByTestId("pricing-tab-audit").dispatchEvent("click");
  await expect(page.getByTestId("pricing-tab-audit")).toHaveAttribute("aria-selected", "true");
});

test("the Quotes desk tab holds once while the log row is owed, then goes", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  await page.getByTestId("desk-tab-quotes").dispatchEvent("click");
  await expect(page.getByTestId("pricing-leave-warning")).toBeVisible();
  await expect(page.getByTestId("pricing-panel")).toBeVisible();
  await page.getByTestId("desk-tab-quotes").dispatchEvent("click");
  await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
});

test("the desk's sign out holds once while the log row is owed, then goes", async ({ page }) => {
  await openRules(page);
  await setMode(page, { fail_confirm: true });
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-retry-log")).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).dispatchEvent("click");
  await expect(page.getByTestId("pricing-leave-warning")).toBeVisible();
  await expect(page.getByTestId("pricing-panel")).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).dispatchEvent("click");
  await expect(page.getByTestId("pricing-panel")).toHaveCount(0);
});

test("two quick Edit clicks open the later one, not the slower earlier answer", async ({ page }) => {
  await openRules(page);
  const first = page.getByTestId("pr-row").filter({ hasText: "Contractor, Pavers" });
  const second = page.getByTestId("pr-row").filter({ hasText: "Contractor, default" });
  const firstId = await first.getByTestId("pr-edit").getAttribute("data-id");
  let release: () => void = () => undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/api/v1/pricing/rules/${firstId}`, async (route) => {
    await gate;
    await route.continue();
  });
  const slow = page.waitForResponse((r) => r.url().endsWith(`/api/v1/pricing/rules/${firstId}`));
  await first.getByTestId("pr-edit").click();
  await second.getByTestId("pr-edit").click();
  await expect(page.getByTestId("pr-detail")).toHaveValue("Contractor, default: 15% off");
  release();
  // the slower, earlier answer lands, and the form is still the later click's
  await slow;
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
  await expect(page.getByTestId("pr-detail")).toHaveValue("Contractor, default: 15% off");
});

// ── the log: names for products and a BYPASS row's reason ────────────────────────────────────────────

test("a floor confirmation in the log names its products where the names are known", async ({ page }) => {
  await openRules(page);
  await saveNewPaversRule(page, "40");
  await page.getByTestId("floor-confirm").click();
  await expect(page.getByTestId("floor-dialog")).toHaveCount(0);
  await openTab(page, "audit");
  const change = page.getByTestId("pa-change").filter({ hasText: "the editor confirmed a floor warning naming 2 products" });
  await expect(change).toContainText("HPV-60 · Holland Stone 60 mm");
  await expect(change).not.toContainText("a7000000-0000");
});

test("a BYPASS row keeps its reason", async ({ page }) => {
  await page.request.post("/_stub/pricing-seed-bypass");
  await signIn(page);
  await openPricing(page);
  await openTab(page, "audit");
  await expect(page.getByTestId("pa-bypass-reason")).toHaveText("Bypassed because the category's margin policy is switched off");
});

// ── round 2 P2-C: a rule limited to one branch keeps it through an edit ──────────────────────────────

test("editing a branch limited tier default rule keeps its branch", async ({ page }) => {
  await openRules(page);
  const row = page.getByTestId("pr-row").filter({ hasText: "Contractor, default" });
  await expect(row).toContainText("Trenton");
  await row.getByTestId("pr-edit").click();
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  // the tier default scope cannot edit the branch, so the branch it has is shown read only,
  // and the read only value is tied to its label by an explicit relation (a11y, round 3)
  await expect(page.getByTestId("pr-branch")).toHaveCount(0);
  await expect(page.getByTestId("pr-branch-readonly")).toHaveText("Trenton");
  const labelledBy = await page.getByTestId("pr-branch-readonly").getAttribute("aria-labelledby");
  expect(labelledBy).not.toBeNull();
  await expect(page.locator(`#${labelledBy}`)).toHaveText("Branch");
  await page.getByTestId("pr-detail").fill("Contractor, default: 15% off, reviewed");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  const rule = (await pricingState(page)).rules.find((r) => r.detail_text === "Contractor, default: 15% off, reviewed");
  expect(rule?.branch_id).toBe("6f1c2b3a-0000-4000-8000-000000000001");
});

// ── round 3 P3: a branch picked on create does not ride a scope switch ──────────────────────────────

test("a branch picked under the tier and category scope is dropped when a create switches scope", async ({ page }) => {
  await openRules(page);
  await page.getByTestId("pr-new").click();
  await page.getByTestId("pr-scope").selectOption("TIER_CATEGORY");
  await page.getByTestId("pr-branch").selectOption("6f1c2b3a-0000-4000-8000-000000000001");
  await page.getByTestId("pr-scope").selectOption("TIER_DEFAULT");
  // the tier default editor cannot hold or clear a branch, so the pick is gone rather than
  // riding along read only and quietly limiting the new rule to that yard
  await expect(page.getByTestId("pr-branch")).toHaveCount(0);
  await expect(page.getByTestId("pr-branch-readonly")).toHaveCount(0);
  await page.getByTestId("pr-value").fill("5");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  const made = (await pricingState(page)).rules.at(-1);
  expect(made?.branch_id).toBe("");
});

// ── round 2 P3: the start date holds through an edit on a core not hosted in UTC ─────────────────────

test("a rule's start date is the day typed when the core serves it in a non UTC offset", async ({ page }) => {
  await openRules(page);
  const edit = page.getByTestId("pr-row").filter({ hasText: "Contractor, default" }).getByTestId("pr-edit");
  await edit.click();
  await page.getByTestId("pr-valid-from").fill("2026-11-15");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("pr-dialog")).toHaveCount(0);
  // the stub stores the typed day as its UTC midnight and serves it at a UTC-8 offset
  const rule = async () => (await pricingState(page)).rules.find((r) => r.detail_text === "Contractor, default: 15% off");
  expect((await rule())?.valid_from).toBe("2026-11-15T00:00:00Z");
  await edit.click();
  await expect(page.getByTestId("pr-valid-from")).toHaveValue("2026-11-15");
});
