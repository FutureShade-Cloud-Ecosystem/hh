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

test("a refused filter leaves the filters on the glass, and Clear brings the trail back", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator("[data-test=audit-count]")).toHaveText("3 entries");

  // An actor id that is not a UUID is refused in the server's words; the
  // surface must not become a dead end.
  await page.locator('[data-field="audit-actor-id"]').fill("not-a-uuid");
  await page.locator('[data-action="apply-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-error]")).toContainText("actor_id must be a UUID");
  await expect(page.locator('[data-field="audit-actor-id"]')).toHaveValue("not-a-uuid");

  await page.locator('[data-action="clear-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-count]")).toHaveText("3 entries");
});

test("a core that has no staff register yet lands on the waits-for-sign-in state, not a raw 404", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "unregistered-staff" } });
  await page.goto("/");
  await page.locator("[data-flow]").first().click();

  await expect(page.locator("[data-test=waits-for-sign-in]")).toContainText("Waits for sign in");
  await expect(page.locator("[data-test=waits-words]")).not.toContainText(/lane/i);
  await expect(page.locator("body")).not.toContainText("404");
  await expect(page.locator("[data-test=staff-error]")).toHaveCount(0);
  await page.request.post("/_stub/reset");
});

// -- the role configuration surface ------------------------------------------------------------------

async function openRoleConfig(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="role-config"]').click();
}

test("the role grid reads every job shape on every surface, and the catalog names who holds each shape", async ({ page }) => {
  await openRoleConfig(page);

  // Fifty cells, each answering the registry default until a curation is
  // saved; the caption carries the core's own sentence about what curation
  // is and is not.
  await expect(page.locator("[data-test^=config-cell-]")).toHaveCount(50);
  await expect(page.locator("[data-test=config-cell-counter-till-counter]")).toHaveText("Registry default");
  await expect(page.locator("[data-test=config-caption]")).toContainText("never grants power");

  // The catalog groups the dealer's roles under their seeded shapes.
  await expect(page.locator("[data-test=config-roles-count]")).toHaveText("14 roles");
  const adminRow = page.locator('[data-test=config-catalog] li[data-profile="admin"]');
  await expect(adminRow).toContainText("General Manager");
  const counterRow = page.locator('[data-test=config-catalog] li[data-profile="counter"]');
  await expect(counterRow).toContainText("Inside Sales");
});

test("curate one cell, save, and the grid cell counts the row", async ({ page }) => {
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();
  await expect(page.locator("[data-test=config-editor]")).toContainText("What the Yard sees on Counter Till");
  await expect(page.locator("[data-test=config-draft-empty]")).toContainText("Nothing curated here yet");

  // The dealer's curation: hide the cost column for the yard shape. The id
  // is the counter's own registry's id; the editor's words say so.
  await page.locator('[data-action="config-add-kind"]').selectOption("column");
  await page.locator('[data-field="config-add-id"]').fill("cost");
  await page.locator('[data-action="config-add-row"]').click();
  await expect(page.locator("[data-test=config-draft-rows]")).toContainText("cost");

  await page.locator('[data-action="config-save"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("Saved");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toHaveText("1 column");

  // Reset the kind: the level beneath answers again, and the cell says so.
  await page.locator('[data-action="config-reset-kind"][data-kind="column"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("Reset");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toHaveText("Registry default");
});

test("your own view saves to the my-items route and reset brings the dealer's back", async ({ page }) => {
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-counter-till-counter"]').click();

  await page.locator('[data-action="config-my-add-kind"]').selectOption("chip");
  await page.locator('[data-field="config-my-add-id"]').fill("pavers");
  await page.locator('[data-action="config-my-add-row"]').click();
  await page.locator('[data-action="config-save-my"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("your own set");
  await expect(page.locator("[data-test=config-my-rows]")).toContainText("pavers");

  await page.locator('[data-action="config-reset-my"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("You follow the dealer's curation again");
});

test("the workspace save lands on the my-workspace route and the reset returns the dealer's defaults", async ({ page }) => {
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-quote-desk-counter"]').click();

  // The split rides the model; a value the core would refuse is refused in
  // the core's own words before the write.
  await page.locator('[data-field="configws-split"]').fill("90");
  await page.locator('[data-action="configws-save"]').click();
  await expect(page.locator("[data-test=config-save-error]")).toContainText("Workspace split must be between 20 and 80");

  await page.locator('[data-field="configws-split"]').fill("40");
  await page.locator('[data-action="configws-save"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("Your workspace");
  await expect(page.locator("[data-test=config-editor]")).toContainText("your own row");

  await page.locator('[data-action="configws-reset"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("the dealer's defaults again");
});

test("a frontline caller is refused the role grid, in the core's own words", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow]").nth(1).click();
  await expect(page.locator("[data-test=staff-error]")).toContainText("managers and above");
  await page.locator('.surface-link[data-surface="role-config"]').click();

  // The kit's service refusal, whole: who cannot, and who can.
  await expect(page.locator("[data-test=config-error]")).toContainText("changing dealer screen settings is limited to administrators");
  await expect(page.locator("[data-test=config-error]")).toContainText("ask a tech admin");
});

test("a refused curation write keeps the editor and says the server's words", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "refused-role-config" } });
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();
  await page.locator('[data-action="config-add-kind"]').selectOption("column");
  await page.locator('[data-field="config-add-id"]').fill("cost");
  await page.locator('[data-action="config-add-row"]').click();
  await page.locator('[data-action="config-save"]').click();

  await expect(page.locator("[data-test=config-save-error]")).toContainText("limited to administrators");
  await expect(page.locator("[data-test=config-editor]")).toBeVisible();
  await page.request.post("/_stub/reset");
});
