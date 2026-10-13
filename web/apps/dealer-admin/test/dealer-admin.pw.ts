// Dealer Admin's Playwright legs: the demo mint, the staff register, and
// the walls. The stub serves the demo routes and the staff world; the
// scenarios move the world (the stub's own controls) between legs.
import { test, expect } from "@playwright/test";

const YARD_TEAM_ROW = "da110000-0000-4000-9000-0000000000yt";

test("demo mint and the staff register", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  // The dealer admin's card is the first; inside sales is the refusal walk's.
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(2);
  await cards.first().click();

  // The app's own left list names the surfaces; Staff is first.
  await expect(page.locator(".surface-link").first()).toContainText("Staff");

  // The register renders with its count, the grant chips by yard name, and
  // the platform row read-only.
  await expect(page.locator("[data-test=staff-count]")).toHaveText("5 staff");
  const yardTeamRow = page.locator(`tr[data-user=${YARD_TEAM_ROW}]`);
  await expect(yardTeamRow).toContainText("Point Loma");
  await expect(yardTeamRow).toContainText("Julian");
  const platformRow = page.locator('tr[data-user="da110000-0000-4000-9000-0000000000sa"]');
  await expect(platformRow).toContainText("Platform Administrator");
  await expect(platformRow.locator("select")).toHaveCount(0);

  // The create form stands disabled in its waits-for-sign-in state.
  await expect(page.locator("[data-test=waits-for-sign-in]")).toContainText("Waits for sign in");

  // The roles catalog renders the dealer's fourteen roles.
  await expect(page.locator("[data-test=roles-count]")).toHaveText("14 roles");
  await expect(page.locator(".role-list")).toContainText("General Manager");
  await expect(page.locator(".role-list")).toContainText("All branches");
});

test("assign a role and request a reset link", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toHaveText("5 staff");

  // The yard team member moves to Yard Manager; the register re-reads and
  // the row's select shows the new role.
  await page.locator(`select[data-user-id=${YARD_TEAM_ROW}]`).selectOption({ label: "Yard Manager" });
  await expect(page.locator(`select[data-user-id=${YARD_TEAM_ROW}]`)).toHaveValue("Yard Manager");

  // A reset link request mints the single use link, shown to copy.
  const insideSalesRow = page.locator('tr[data-user="da110000-0000-4000-9000-000000000002"]');
  await insideSalesRow.getByRole("button", { name: "Reset link" }).click();
  await expect(page.locator("[data-test=reset-link]").first()).toContainText("reset.hh-demo.invalid");
});

test("a frontline caller is refused the staff read, and the refusal names who may act", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.nth(1).click();

  // The register is a manager's read: the inside sales card lands refused,
  // in the words naming who may act, and no count is asserted.
  await expect(page.locator("[data-test=staff-error]")).toContainText("managers and above");
});

test("the two hotkey rosters, a captured key, and the save", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();

  // The app's left list opens the Hotkeys surface; both rosters render.
  await page.locator('.surface-link[data-surface="hotkeys"]').click();
  await expect(page.locator("[data-test=hotkeys-roster-app\\.shell]")).toBeVisible();
  await expect(page.locator("[data-test=hotkeys-roster-pos\\.counter]")).toBeVisible();
  await expect(page.locator("[data-test=hotkeys-cap-pos\\.counter-pay]")).toHaveText("F12");

  // Arm the counter's pay row. A press that would double claim the shipped
  // roster is refused at the press, named by the action that holds the key,
  // and the capture stays armed for the next press.
  await page.locator('[data-action="ph-set"][data-surface="pos.counter"][data-key-action="pay"]').click();
  await expect(page.locator('[data-action="ph-set"][data-surface="pos.counter"][data-key-action="pay"]')).toHaveText("Press a key");
  await page.keyboard.press("F4");
  await expect(page.locator("[data-test=hotkeys-refusal-pos\\.counter]")).toContainText("Move the sale on to the next step");

  // A free key lands: the chord takes the row and the capture is done.
  await page.keyboard.press("F5");
  await expect(page.locator("[data-test=hotkeys-cap-pos\\.counter-pay]")).toHaveText("F5");

  // Save the roster; the stub answers saved and the read re-renders it.
  await page.locator('[data-action="ph-save"][data-surface="pos.counter"]').click();
  await expect(page.locator("[data-test=hotkeys-notice]")).toContainText("Saved");
  await expect(page.locator("[data-test=hotkeys-cap-pos\\.counter-pay]")).toHaveText("F5");
});

test("a caller who may not edit the rosters reads them read-only", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "readonly-hotkeys" } });
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await page.locator('.surface-link[data-surface="hotkeys"]').click();

  // The server's can_edit gates the write: the calm line and no Set buttons.
  await expect(page.locator("[data-test=hotkeys-readonly]")).toBeVisible();
  await expect(page.locator('[data-action="ph-set"]')).toHaveCount(0);
  await expect(page.locator('[data-action="ph-save"]')).toHaveCount(0);
  await page.request.post("/_stub/reset");
});

test("the audit trail, one entry's masked detail, and the entity's whole trail", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="audit"]').click();

  // The trail renders three entries; the sensitive one is badged.
  await expect(page.locator("[data-test=audit-count]")).toHaveText("3 entries");
  const saleRow = page.locator("[data-test^=audit-row-]").filter({ hasText: "SALE_COMPLETED" });
  const overrideRow = page.locator("[data-test^=audit-row-]").filter({ hasText: "PRICE_OVERRIDE" });
  await expect(overrideRow.locator(".chip-sensitive")).toBeVisible();

  // A row opens the masked detail drawer: the change set is the projection.
  await overrideRow.click();
  await expect(page.locator("[data-detail-state=ready]")).toBeVisible();
  await expect(page.locator("[data-test=audit-changes]")).toContainText("contract price match");

  // The sale's whole trail opens beneath: the sale spans two rows.
  await saleRow.click();
  await expect(page.locator("[data-detail-state=ready]")).toBeVisible();
  await page.locator('[data-action="open-audit-trail"]').click();
  await expect(page.locator("[data-test=audit-trail]")).toBeVisible();
  await expect(page.locator("[data-test^=audit-trail-row-]")).toHaveCount(2);
});

test("the audit filters narrow the trail, and empty means two different things", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator("[data-test=audit-count]")).toHaveText("3 entries");

  // The action filter narrows to the one override.
  await page.locator('[data-field="audit-action"]').selectOption({ label: "PRICE_OVERRIDE" });
  await page.locator('[data-action="apply-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-count]")).toHaveText("1 entries");

  // An empty trail under active filters says the filters are narrower than
  // the record, not that nothing happened.
  await page.locator('[data-field="audit-entity-type"]').fill("warranty.claim");
  await page.locator('[data-action="apply-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-empty]")).toContainText("narrower than the record");

  // Clear empties the fields and the trail returns whole.
  await page.locator('[data-action="clear-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-count]")).toHaveText("3 entries");
  await expect(page.locator('[data-field="audit-entity-type"]')).toHaveValue("");
});

test("a frontline caller is refused the audit read", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.nth(1).click();
  await page.locator('.surface-link[data-surface="audit"]').click();

  // The trail is leadership's read; the refusal names who may act and the
  // right surface, not the staff register's words.
  await expect(page.locator("[data-test=audit-error]")).toContainText("leadership");
  await expect(page.locator("[data-test=audit-error]")).not.toContainText("staff register");
});

test("a refused role change says so beside the register", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "refused-assign" } });
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toHaveText("5 staff");

  // The server refuses the write: the select snaps back and the words stay
  // on the glass beside the register, in the ready phase.
  await page.locator(`select[data-user-id=${YARD_TEAM_ROW}]`).selectOption({ label: "Yard Manager" });
  await expect(page.locator("[data-test=assign-error]")).toContainText("The role did not change.");
  await expect(page.locator("[data-test=staff-count]")).toHaveText("5 staff");
  await page.request.post("/_stub/reset");
});

async function openSaleTrail(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await page.locator('.surface-link[data-surface="audit"]').click();
  await page.locator("[data-test^=audit-row-]").filter({ hasText: "SALE_COMPLETED" }).click();
  await expect(page.locator("[data-detail-state=ready]")).toBeVisible();
  await page.locator('[data-action="open-audit-trail"]').click();
}

test("the entity's trail reads the bare array the real core writes", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "bare-trail" } });
  await openSaleTrail(page);
  await expect(page.locator("[data-test^=audit-trail-row-]")).toHaveCount(2);
  await page.request.post("/_stub/reset");
});

test("an entity trail with no entries says so in its own words", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "empty-trail" } });
  await openSaleTrail(page);
  await expect(page.locator("[data-test=audit-trail-empty]")).toContainText("holds no entries");
  await page.request.post("/_stub/reset");
});
