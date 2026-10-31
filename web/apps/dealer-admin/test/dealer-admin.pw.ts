// Dealer Admin's Playwright legs: the demo mint, the staff register, and
// the walls. The stub serves the demo routes and the staff world; the
// scenarios move the world (the stub's own controls) between legs.
import { test, expect } from "@playwright/test";

const YARD_TEAM_ROW = "da110000-0000-4000-9000-0000000000yt";

test("demo mint and the staff register", async ({ page }) => {
  await page.goto("/");
  // PR 503 round 1: the band is the ONE band; the app-local header is gone.
  await expect(page.locator("hh-shell-header")).toBeVisible();
  await expect(page.locator("hos-app-header")).toHaveCount(0);
  // The dealer admin's card is the first; inside sales is the refusal walk's.
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(2);
  await cards.first().click();

  // The app's own left list names the surfaces; Staff is first.
  await expect(page.locator(".surface-link").first()).toContainText("Staff");

  // The register renders with its count, the grant chips by yard name, and
  // the platform row read-only.
  await expect(page.locator("[data-test=staff-count]")).toHaveText("7 staff");
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
  await expect(page.locator("[data-test=staff-count]")).toHaveText("7 staff");

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
  await expect(page.locator("[data-test=staff-count]")).toHaveText("7 staff");

  // The server refuses the write: the select snaps back and the words stay
  // on the glass beside the register, in the ready phase.
  await page.locator(`select[data-user-id=${YARD_TEAM_ROW}]`).selectOption({ label: "Yard Manager" });
  await expect(page.locator("[data-test=assign-error]")).toContainText("The role did not change.");
  await expect(page.locator("[data-test=staff-count]")).toHaveText("7 staff");
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

  // An entity type with spaces is refused in the server's words; the
  // surface must not become a dead end.
  await page.locator('[data-field="audit-entity-type"]').fill("bad entity");
  await page.locator('[data-action="apply-audit-filters"]').click();
  await expect(page.locator("[data-test=audit-error]")).toContainText("entity_type must not contain spaces");
  await expect(page.locator('[data-field="audit-entity-type"]')).toHaveValue("bad entity");

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

  // A hundred and thirty cells (thirteen profiles on ten surfaces), each answering the registry default until a curation is
  // saved; the caption carries the core's own sentence about what curation
  // is and is not.
  await expect(page.locator("[data-test^=config-cell-]")).toHaveCount(130);
  await expect(page.locator("[data-test=config-cell-counter-till-counter]")).toHaveText("Standard view");
  await expect(page.locator("[data-test=config-caption]")).toContainText("never grants power");

  // The catalog groups the dealer's roles under their seeded shapes.
  await expect(page.locator("[data-test=config-roles-count]")).toHaveText("14 roles");
  const adminRow = page.locator('[data-test=config-catalog] li[data-profile="manager.general"]');
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
  await page.locator('[data-field="config-add-id"]').selectOption("cost");
  await page.locator('[data-action="config-add-row"]').click();
  await expect(page.locator("[data-test=config-draft-rows]")).toContainText("cost");

  await page.locator('[data-action="config-save"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("Saved");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toHaveText("1 column");

  // Reset the kind: the level beneath answers again, and the cell says so.
  await page.locator('[data-action="config-reset-kind"][data-kind="column"]').click();
  await expect(page.locator("[data-test=config-notice]")).toContainText("Reset");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toHaveText("Standard view");
});

test("your own view saves to the my-items route and reset brings the dealer's back", async ({ page }) => {
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-counter-till-counter"]').click();

  await page.locator('[data-action="config-my-add-kind"]').selectOption("chip");
  await page.locator('[data-field="config-my-add-id"]').selectOption("pavers");
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

  // The kit's service refusal, whole: who can.
  await expect(page.locator("[data-test=config-error]")).toContainText("only the General Manager, the Financial Controller, HR or the Platform Administrator see another profile's dealer screen settings");
});

test("a refused curation write keeps the editor and says the server's words", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "refused-role-config" } });
  await openRoleConfig(page);
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();
  await page.locator('[data-action="config-add-kind"]').selectOption("column");
  await page.locator('[data-field="config-add-id"]').selectOption("cost");
  await page.locator('[data-action="config-add-row"]').click();
  await page.locator('[data-action="config-save"]').click();

  await expect(page.locator("[data-test=config-save-error]")).toContainText("only the General Manager, the Financial Controller, HR or the Platform Administrator change the dealer screen settings");
  await expect(page.locator("[data-test=config-editor]")).toBeVisible();
  await page.request.post("/_stub/reset");
});

// -- the deployment settings surface -----------------------------------------------------------------

async function openDeployment(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="deployment"]').click();
}

test("the deployment settings render their four sections, and the brand waits for the platform", async ({ page }) => {
  await openDeployment(page);

  // The brand pair is registered and not yet served: the waits state, in
  // plain words, with the form disabled.
  await expect(page.locator("[data-test=brand-waits]")).toContainText("Waits for the platform");
  await expect(page.locator("[data-test=brand-waits-words]")).toContainText("brand record");
  await expect(page.locator('[data-field="brand-name"]')).toBeDisabled();

  // The card rail answers: configured, in test mode, the sandbox named.
  await expect(page.locator("[data-test=payments-status]")).toContainText("gls-demo-rail");
  await expect(page.locator("[data-test=payments-simulated]")).toContainText("Test mode");

  // The counter's policy answers with its knobs, and the history carries
  // the seeded change.
  await expect(page.locator('[data-field="policy-fee"]')).toHaveValue("250");
  await expect(page.locator("[data-test=policy-history]")).toBeVisible();
  await expect(page.locator("[data-test=policy-history]")).toContainText("restocking fee");

  // The walkthrough answers nothing on a fresh dealer: the off words, and
  // the platform gate's calm line in place of the switches.
  await expect(page.locator("[data-test=tour-off]")).toContainText("has it off");
  await expect(page.locator("[data-test=tour-readonly]")).toContainText("platform administrator's own");
});

test("the card rail section is read only: no form, no save, and the credentials are never shown", async ({ page }) => {
  await openDeployment(page);
  await expect(page.locator("[data-test=payments-simulated]")).toContainText("Test mode");
  await expect(page.locator("[data-test=payments-readonly]")).toContainText("set at the dealer's implementation");
  await expect(page.locator("[data-test=payments-readonly]")).toContainText("write only and never shown");
  await expect(page.locator('[data-field^="pay-"]')).toHaveCount(0);
  await expect(page.locator('[data-action="settings-save-payments"]')).toHaveCount(0);
});

test("the counter's policy saves whole, and its history grows by one row per changed knob", async ({ page }) => {
  await openDeployment(page);
  await expect(page.locator("[data-test=policy-history-count]")).toHaveText("1 changes");

  // A fee the core would refuse is refused in words before the write, and
  // the save stays dark while the fault stands.
  await page.locator('[data-field="policy-fee"]').fill("-5");
  await expect(page.locator("[data-test=policy-fault]")).toContainText("cannot be negative");
  await expect(page.locator('[data-action="settings-save-policy"]')).toBeDisabled();

  await page.locator('[data-field="policy-fee"]').fill("300");
  await page.locator('[data-field="policy-notes"]').fill("Yard skids only");
  await page.locator('[data-action="settings-save-policy"]').click();
  await expect(page.locator("[data-test=policy-notice]")).toContainText("Saved");
  await expect(page.locator('[data-field="policy-fee"]')).toHaveValue("300");

  // The trail answers the read: one row per changed knob, before to after.
  await expect(page.locator("[data-test=policy-history-count]")).toHaveText("3 changes");
  const notesRow = page.locator("[data-test=policy-history-row]").filter({ hasText: "notes" });
  await expect(notesRow).toContainText("Yard skids only");
  await page.request.post("/_stub/reset");
});

test("a frontline caller reads the counter's policy but is refused the write and the trail", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow]").nth(1).click();
  await expect(page.locator("[data-test=staff-error]")).toContainText("managers and above");
  await page.locator('.surface-link[data-surface="deployment"]').click();

  // The policy read is staff wide: the form stands. The write and the
  // history are the administrators', and both say so.
  await expect(page.locator('[data-field="policy-fee"]')).toBeVisible();
  await expect(page.locator("[data-test=policy-history-error]")).toContainText("the administrators' own");
  await page.locator('[data-action="settings-save-policy"]').click();
  await expect(page.locator("[data-test=policy-save-error]")).toContainText("You may not set the counter's policy");
});

test("the walkthrough live: the chapters list, the platform switches, and a step's copy saved", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "tour-platform" } });
  await openDeployment(page);

  // The chapters render, the platform gate opens the switches, and the
  // step editor carries the picked step's stored copy.
  await expect(page.locator("[data-test=tour-chapters]")).toContainText("Your first sale at the counter");
  await expect(page.locator('[data-action="settings-tour-demo-off"]')).toBeVisible();
  await expect(page.locator('[data-field="tour-line"]')).toHaveValue("Find the pavers the customer wants.");

  await page.locator('[data-field="tour-line"]').fill("Find the wall block the customer wants.");
  await page.locator('[data-action="settings-save-tour-step"]').click();
  await expect(page.locator("[data-test=tour-notice]")).toContainText("The step says your line");
  await page.request.post("/_stub/reset");
});

test("the walkthrough's copy refuses a line over the headline budget, in the core's words", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "tour-platform" } });
  await openDeployment(page);

  await page.locator('[data-field="tour-line"]').fill("x".repeat(73));
  await page.locator('[data-action="settings-save-tour-step"]').click();
  await expect(page.locator("[data-test=tour-save-error]")).toContainText("a walkthrough speaks in headlines");
  await page.request.post("/_stub/reset");
});

test("the platform gate's refusal arrives in the core's own sentence, whole", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "tour-refused" } });
  await openDeployment(page);

  // The read says the caller may edit (a stale answer), so the switches
  // stand; the write answers the platform gate's own words and the glass
  // carries them whole.
  await expect(page.locator('[data-action="settings-tour-demo-off"]')).toBeVisible();
  await page.locator('[data-action="settings-tour-demo-off"]').click();
  await expect(page.locator("[data-test=tour-save-error]")).toContainText("the walkthrough is the platform administrator's to set up");
  await expect(page.locator("[data-test=tour-save-error]")).toContainText("cannot change its copy or its demo switch");
  await page.request.post("/_stub/reset");
});

async function openWarranty(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="warranty"]').click();
}

test("the warranty surface: the threshold reads and saves in the board's words", async ({ page }) => {
  await openWarranty(page);

  // The threshold answers at the 0396 seed, the field carrying the core's
  // own dollars word.
  await expect(page.locator(".page-head h1")).toHaveText("Warranty");
  await expect(page.locator(".page-sub")).toContainText("When a defect becomes a warranty claim");
  await expect(page.locator('[data-field="warranty-threshold"]')).toHaveValue("$50.00");

  // A half typed amount is a fault named before the write, and the save
  // stays dark while it stands.
  await page.locator('[data-field="warranty-threshold"]').fill("$25.0.0");
  await expect(page.locator("[data-test=warranty-threshold-fault]")).toContainText("like $50.00");
  await expect(page.locator('[data-action="warranty-save-threshold"]')).toBeDisabled();

  await page.locator('[data-field="warranty-threshold"]').fill("$25");
  await page.locator('[data-action="warranty-save-threshold"]').click();
  await expect(page.locator("[data-test=warranty-threshold-notice]")).toContainText("Saved");
  await expect(page.locator('[data-field="warranty-threshold"]')).toHaveValue("$25.00");

  // The answer is the server's: the surface re-reads what the core holds.
  await page.locator('.surface-link[data-surface="staff"]').click();
  await page.locator('.surface-link[data-surface="warranty"]').click();
  await expect(page.locator('[data-field="warranty-threshold"]')).toHaveValue("$25.00");
  await page.request.post("/_stub/reset");
});

test("the vendor agreements render the boards' states, and a kind switch lands in one click", async ({ page }) => {
  await openWarranty(page);

  const rows = page.locator("[data-test=warranty-vendor-row]");
  await expect(rows).toHaveCount(4);
  const copper = rows.filter({ hasText: "Copperline Pavers" });
  const heron = rows.filter({ hasText: "Heronfield Stone" });
  const prairie = rows.filter({ hasText: "Prairie Bend Soils" });

  // The stub's world matches the boards: Copperline claim by claim with no
  // period, Heronfield a quarterly fixed allowance at 1%, Prairie Bend a
  // monthly periodic credit note.
  await expect(copper.locator('[data-kind="claim_by_claim"]')).toHaveAttribute("aria-pressed", "true");
  await expect(copper.locator('select[data-action="warranty-period"]')).toHaveCount(0);
  await expect(heron.locator('[data-kind="fixed_allowance"]')).toHaveAttribute("aria-pressed", "true");
  await expect(heron.locator('select[data-action="warranty-period"]')).toHaveValue("quarterly");
  await expect(heron.locator('[data-field="warranty-allowance"]')).toHaveValue("1%");
  await expect(prairie.locator('[data-kind="periodic_credit_note"]')).toHaveAttribute("aria-pressed", "true");
  await expect(prairie.locator('select[data-action="warranty-period"]')).toHaveValue("monthly");

  // One click on Fixed allowance lands Copperline on a sane, server-legal
  // row: the standard quarterly and the board's 1% default beside it.
  await copper.locator('[data-kind="fixed_allowance"]').click();
  await expect(copper.locator('[data-kind="fixed_allowance"]')).toHaveAttribute("aria-pressed", "true");
  await expect(copper.locator('select[data-action="warranty-period"]')).toHaveValue("quarterly");
  await expect(copper.locator('[data-field="warranty-allowance"]')).toHaveValue("1%");

  // The percent writes on change: 2% sticks, and the field reads it back.
  await copper.locator('[data-field="warranty-allowance"]').fill("2%");
  await copper.locator('[data-field="warranty-allowance"]').blur();
  await expect(copper.locator('[data-field="warranty-allowance"]')).toHaveValue("2%");

  // Off-vocabulary words are named on the row, and the stored 2% stands.
  await copper.locator('[data-field="warranty-allowance"]').fill("1 of purchases");
  await copper.locator('[data-field="warranty-allowance"]').blur();
  await expect(copper.locator("[data-test=warranty-row-error]")).toContainText("like 1% of purchases");
  await expect(copper.locator('[data-field="warranty-allowance"]')).toHaveValue("2%");

  // Back to claim by claim: the period and the allowance are gone, the
  // server's read-back says the row carries nothing else.
  await copper.locator('[data-kind="claim_by_claim"]').click();
  await expect(copper.locator('select[data-action="warranty-period"]')).toHaveCount(0);
  await expect(copper.locator('[data-field="warranty-allowance"]')).toHaveCount(0);
  await page.request.post("/_stub/reset");
});

test("a frontline caller reads the warranty surface but is refused every write", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow]").nth(1).click();
  await page.locator('.surface-link[data-surface="warranty"]').click();

  // The reads are staff's: the rows stand. The writes are the dealer's
  // administrators' own, and the refusal says so on the row it belongs to.
  const copper = page.locator("[data-test=warranty-vendor-row]").filter({ hasText: "Copperline Pavers" });
  await expect(copper).toBeVisible();
  await copper.locator('[data-kind="fixed_allowance"]').click();
  await expect(copper.locator("[data-test=warranty-row-error]")).toContainText("the dealer's administrators' own");
  await page.request.post("/_stub/reset");
});

test("the pricing surface: the margin policies, and an edit saved clean", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();

  // The strip names the thirteen views: the twelve tabs and the payment terms
  // view, one axis after UX-106 merged the two strips. The margin view is the
  // open one.
  await expect(page.locator(".pr-tab")).toHaveCount(13);
  await expect(page.locator("[data-test=pr-margin-count]")).toHaveText("1 policy");
  const row = page.locator('tr[data-category="ca7a0000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("Pavers");
  await expect(row.locator("[data-test=pr-margin-target]")).toHaveText("35.00%");
  await expect(row.locator("[data-test=pr-margin-floor]")).toHaveText("20.00%");

  // An edit saves straight through a clean grading: the write lands, the
  // read re-renders the server's figure, and nothing was queued.
  await row.getByRole("button", { name: "Edit the margin policy for Pavers" }).click();
  await page.locator('[data-field="pr-policy-target"]').fill("36");
  await page.locator('[data-action="pr-policy-save"]').click();
  await expect(page.locator("[data-test=pr-margin-notice]")).toContainText("Saved.");
  await expect(page.locator('tr[data-category="ca7a0000-0000-4000-9000-0000000000a1"] [data-test=pr-margin-target]')).toHaveText("36.00%");
});

test("the pricing floor warning: the server's grading opens the panel, the confirm writes and logs", async ({ page }) => {
  await page.request.post("/_stub/scenario", { data: { name: "floor-warning" } });
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator("[data-test=pr-margin-count]")).toHaveText("1 policy");

  // The save asks the floor first; the warning opens with the server's own
  // items, and nothing has been written yet.
  const row = page.locator('tr[data-category="ca7a0000-0000-4000-9000-0000000000a1"]');
  await row.getByRole("button", { name: "Edit the margin policy for Pavers" }).click();
  await page.locator('[data-field="pr-policy-floor"]').fill("10");
  await page.locator('[data-action="pr-policy-save"]').click();
  const panel = page.locator("[data-test=pr-floor-panel]");
  await expect(panel).toContainText("The floor check names 1 product");
  await expect(panel).toContainText("PAV-60-CH");
  await expect(panel).toContainText("15.00%");

  // Confirming runs the write, then logs the confirmation; the verdict is
  // the server's own word.
  await panel.getByRole("button", { name: "Save anyway, and log that I saw this" }).click();
  await expect(page.locator("[data-test=pr-margin-notice]")).toContainText("logged");
  await expect(page.locator('tr[data-category="ca7a0000-0000-4000-9000-0000000000a1"] [data-test=pr-margin-floor]')).toHaveText("10.00%");
  await page.request.post("/_stub/reset");
});

test("a pricing write is refused for a frontline caller, in the server's own words", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.nth(1).click();
  await page.locator('.surface-link[data-surface="pricing"]').click();

  // The pricing reads are staff's, so the policies load; the write is the
  // manager tier's, and the refusal keeps the server's word in it.
  await expect(page.locator("[data-test=pr-margin-count]")).toHaveText("1 policy");
  const row = page.locator('tr[data-category="ca7a0000-0000-4000-9000-0000000000a1"]');
  await row.getByRole("button", { name: "Edit the margin policy for Pavers" }).click();
  await page.locator('[data-field="pr-policy-target"]').fill("40");
  await page.locator('[data-action="pr-policy-save"]').click();
  await expect(page.locator("[data-test=pr-policy-error]")).toContainText("Forbidden");
  await expect(page.locator("[data-test=pr-policy-error]")).toContainText("manager");
  await expect(page.locator("[data-test=pr-margin-count]")).toHaveText("1 policy");
});

test("the rules tab: the list, an edit saved clean, and a retire asked twice", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="rules"]').click();
  await expect(page.locator("[data-test=pr-rules-count]")).toHaveText("1 rule");

  // The rule reads as a person reads it: the scope word, the value in its
  // own unit, the category by name.
  const row = page.locator('tr[data-rule="9a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("Tier + category");
  await expect(row).toContainText("Pavers");
  await expect(row.locator("[data-test=pr-rule-value]")).toHaveText("10.00% off list");

  // An edit re-reads the rule and saves through a clean grading.
  await row.getByRole("button", { name: "Edit the rule Trade discount" }).click();
  await expect(page.locator("[data-test=pr-rule-editor]")).toContainText("version 3");
  await page.locator('[data-field="pr-rule-discount"]').fill("12");
  await page.locator('[data-action="pr-rule-save"]').click();
  await expect(page.locator("[data-test=pr-rules-notice]")).toContainText("Saved.");
  await expect(page.locator('tr[data-rule="9a110000-0000-4000-9000-0000000000a1"] [data-test=pr-rule-value]')).toHaveText("12.00% off list");

  // Retire asks twice; the second tap carries the row's version.
  const retire = page.locator('[data-action="pr-rule-retire"][data-id="9a110000-0000-4000-9000-0000000000a1"]');
  await retire.click();
  await expect(retire).toHaveText("Retire: tap again");
  await retire.click();
  await expect(page.locator("[data-test=pr-rules-notice]")).toContainText("Retired.");
  await expect(page.locator('tr[data-rule="9a110000-0000-4000-9000-0000000000a1"]')).toContainText("Retired");
});

test("the customer prices tab: one account's overrides, an add, and a delete asked twice", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="customers"]').click();

  // Before a customer is named, the empty says what this tab is for.
  await expect(page.locator("[data-test=pr-cust-empty]")).toContainText("Choose a customer");

  // Load the yard account: one price on Retaining Walls, named by category.
  await page.locator('[data-picker-search="pr-cust-id"]').fill("Point Loma");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-cust-id"]').first().click();
  await page.locator('[data-action="pr-cust-load"]').click();
  await expect(page.locator("[data-test=pr-cust-count]")).toHaveText("1 price");
  const row = page.locator('tr[data-override="8a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("Retaining Walls");
  await expect(row.locator("[data-test=pr-cust-value]")).toHaveText("8.00% off list");

  // An add: the card's own New price button opens the blank editor; pick
  // the category, the calc, the percent, save through a clean grading.
  await page.locator('[data-action="pr-cust-edit"]').first().click();
  await page.locator("[data-test=pr-cust-editor] [data-field='pr-cust-category']").selectOption({ label: "Slabs and Steps" });
  await page.locator('[data-field="pr-cust-calc"]').selectOption({ label: "Percent off list" });
  await page.locator('[data-field="pr-cust-discount"]').fill("5");
  await page.locator('[data-action="pr-cust-save"]').click();
  await expect(page.locator("[data-test=pr-cust-notice]")).toContainText("Saved.");
  await expect(page.locator("[data-test=pr-cust-count]")).toHaveText("2 prices");

  // Delete asks twice, then the row is gone.
  const del = page.locator('[data-action="pr-cust-del"]').first();
  await del.click();
  await expect(del).toHaveText("Delete: tap again");
  await del.click();
  await expect(page.locator("[data-test=pr-cust-notice]")).toContainText("Deleted.");
  await expect(page.locator("[data-test=pr-cust-count]")).toHaveText("1 price");
});

test("the quantity breaks tab: the schedule, a tier saved through the floor, a retire asked twice", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="breaks"]').click();
  await expect(page.locator("[data-test=pr-breaks-count]")).toHaveText("1 schedule");

  const row = page.locator('tr[data-break="7a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("Paver pallet breaks");
  await expect(row).toContainText("Pavers");
  await expect(row).toContainText("10 and up: 5.00% off");

  // The tier's percent edits as a percent; the save rides a clean grading.
  await row.getByRole("button", { name: "Edit the schedule Paver pallet breaks" }).click();
  await expect(page.locator("[data-test=pr-break-editor]")).toContainText("version 2");
  await page.locator('[data-field="pr-brk-item-0-value"]').fill("6");
  await page.locator('[data-action="pr-break-save"]').click();
  await expect(page.locator("[data-test=pr-breaks-notice]")).toContainText("Saved.");
  await expect(page.locator('tr[data-break="7a110000-0000-4000-9000-0000000000a1"]')).toContainText("10 and up: 6.00% off");

  // Retire asks twice.
  const retire = page.locator('[data-action="pr-break-del"][data-id="7a110000-0000-4000-9000-0000000000a1"]');
  await retire.click();
  await expect(retire).toHaveText("Retire: tap again");
  await retire.click();
  await expect(page.locator("[data-test=pr-breaks-notice]")).toContainText("Retired.");
  await expect(page.locator('tr[data-break="7a110000-0000-4000-9000-0000000000a1"]')).toContainText("Retired");
});

test("the promotions and clearance tab: both panels, a promo saved, the badge only mark", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="promos"]').click();

  // The promotions panel: the window reads as days, the value in its unit.
  await expect(page.locator("[data-test=pr-promo-count]")).toHaveText("1 promotion");
  const promoRow = page.locator('tr[data-promo="6a110000-0000-4000-9000-0000000000a1"]');
  await expect(promoRow).toContainText("Summer pavers");
  await expect(promoRow).toContainText("Pavers");
  await expect(promoRow.locator("[data-test=pr-promo-value]")).toHaveText("10.00% off");

  // The clearance panel: the badge only case said plainly.
  await expect(page.locator("[data-test=pr-clear-count]")).toHaveText("1 item");
  const clearRow = page.locator('tr[data-clearance="fa110000-0000-4000-9000-0000000000a1"]');
  await expect(clearRow).toContainText("Display batch");
  await expect(clearRow.locator("[data-test=pr-clear-value]")).toHaveText("badge only, prices through the waterfall");

  // A promotion edit saves straight through: no floor walk for its level.
  await promoRow.getByRole("button", { name: "Edit the promotion Summer pavers" }).click();
  await expect(page.locator("[data-test=pr-promo-editor]")).toContainText("version 4");
  await page.locator('[data-field="pr-prm-value"]').fill("12");
  await page.locator('[data-action="pr-promo-save"]').click();
  await expect(page.locator("[data-test=pr-promo-notice]")).toContainText("Saved.");
  await expect(page.locator('tr[data-promo="6a110000-0000-4000-9000-0000000000a1"] [data-test=pr-promo-value]')).toHaveText("12.00% off");
});

test("the rebates tab: the programs, the vendor's ledger, and an accrual with its verdict", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="rebates"]').click();

  // The program's rate reads in the shape its basis means.
  await expect(page.locator("[data-test=pr-rebate-count]")).toHaveText("1 program");
  const row = page.locator('tr[data-rebate="5a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("Northline Supply");
  await expect(row.locator("[data-test=pr-rebate-rate]")).toHaveText("2.00% of sale");

  // The ledger follows the first vendor: the claim side, with its total.
  await expect(page.locator("[data-test=pr-ledger-total]")).toContainText("$140.00");
  await expect(page.locator("[data-accrual]")).toHaveCount(1);

  // An accrual: the facts in, the server's counts out, and the ledger re-read.
  await page.locator('[data-field="pr-acc-source-id"]').fill("0f110000-0000-4000-9000-0000000000a2");
  await page.locator('[data-picker-search="pr-acc-product"]').fill("Paver 60");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-acc-product"]').first().click();
  await page.locator('[data-field="pr-acc-category"]').selectOption("ca7a0000-0000-4000-9000-0000000000a1");
  await page.locator('[data-field="pr-acc-qty"]').fill("6");
  await page.locator('[data-field="pr-acc-sale"]').fill("3500.00");
  await page.locator('[data-field="pr-acc-cost"]').fill("2500.00");
  await page.locator('[data-action="pr-accrue"]').click();
  await expect(page.locator("[data-test=pr-accrue-result]")).toContainText("1 accrual written");
  await expect(page.locator("[data-test=pr-ledger-total]")).toContainText("$210.00");
});

test("the units of measure tab: the seeded edge, the warn line, and the probe's sentences", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="uom"]').click();

  // The seeded placeholder is named on the row and in the warn line.
  await expect(page.locator("[data-test=pr-uom-warn]")).toContainText("1 of 1 factors are seeded defaults");
  await expect(page.locator("[data-test=pr-uom-count]")).toHaveText("1 edge");
  const row = page.locator('tr[data-conversion="4a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("SF");
  await expect(row).toContainText("unconfirmed");

  // The probe: type the question, read the sentences the counter reads out.
  await page.locator('[data-picker-search="pr-uom-probe-product"]').fill("Paver 60");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-uom-probe-product"]').first().click();
  await page.locator('[data-field="pr-uom-probe-qty"]').fill("77");
  await page.locator('[data-field="pr-uom-probe-uom"]').fill("SF");
  await page.locator('[data-action="pr-uom-plan"]').click();
  const plan = page.locator("[data-test=pr-uom-plan]");
  await expect(plan).toContainText("77 SF = 6.16 PLT");
  await expect(plan).toContainText("picks 7 PLT (rounded up; 0.84 PLT over)");
  await expect(plan).toContainText("rests on unconfirmed factors");
});

test("the batches and imports tab: the staged batch, its lines, the maker check", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="batches"]').click();

  await expect(page.locator("[data-test=pr-batches-count]")).toHaveText("1 batch");
  await expect(page.locator("[data-test=pr-profiles-count]")).toHaveText("1 profile");
  const row = page.locator('tr[data-batch="3a110000-0000-4000-9000-0000000000a1"]');
  await expect(row).toContainText("NL-2026-07");
  await expect(row).toContainText("Staged");

  // Opening the batch shows its lines, the unmatched SKU named.
  await row.getByRole("button", { name: "Lines" }).click();
  await expect(page.locator("[data-test=pr-batch-open]")).toContainText("UNKNOWN-SKU");
  await expect(page.locator("[data-test=pr-batch-open]")).toContainText("no catalog product");

  // Import into the draft's rows: the result is the server's counts.
  await page.locator('[data-field="pr-batch-csv"]').fill("Item,Dealer Net,MSRP\nPAV-60-CH,315,520\nWAL-8,210,340\nSLB-12,180,290\nXX,999,999");
  await page.locator('[data-action="pr-batch-import"]').click();
  await expect(page.locator("[data-test=pr-batch-import-result]")).toContainText("4 rows parsed");

  // The maker check: approve the staged batch.
  await page.locator('[data-action="pr-batch-approve"]').click();
  await expect(page.locator("[data-test=pr-batch-notice]")).toContainText("Approved");
  await expect(page.locator("[data-test=pr-batch-open]")).toContainText("Approved");

  // Then apply it: the answer is the server's count of prices written.
  await page.locator('[data-action="pr-batch-apply"]').click();
  await expect(page.locator("[data-test=pr-batch-notice]")).toContainText("Applied");
  await expect(page.locator("[data-test=pr-batch-open]")).toContainText("Applied");

  // The mapping editor: name the SKU column, save, the profile re-reads.
  await page.locator('[data-action="pr-profile-edit"]').click();
  await page.locator('[data-field="pr-profile-sku"]').fill("SKU");
  await page.locator('[data-action="pr-profile-save"]').click();
  await expect(page.locator("[data-test=pr-batches-notice]")).toContainText("mapping is saved");
});

test("the price file tab: the vendor, the preview with its digest, and the staged apply", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="pricefile"]').click();

  // The vendor reads with its mapping's history.
  await expect(page.locator("[data-test=pr-pf-vendor-count]")).toHaveText("1 vendor");
  await expect(page.locator('tr[data-vendor="vd110000-0000-4000-9000-0000000000a1"]')).toContainText("authored against a real file");
  await expect(page.locator("[data-test=pr-pf-runs-count]")).toHaveText("1 run");

  // Choose the manufacturer, load a CSV, preview the diff.
  await page.locator('[data-field="pr-pf-vendor"]').selectOption({ label: "Northline Supply" });
  await page.locator('[data-field="pr-pf-file"]').setInputFiles({
    name: "nl-pricefile.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("Item,Dealer Net,MSRP\nPAV-60-CH,315,520\nWAL-8,210,340\nSLB-12,180,290\nXX,999,999"),
  });
  await page.locator('[data-action="pr-pf-preview"]').click();
  await expect(page.locator("[data-test=pr-pf-preview]")).toContainText("4 rows");
  await expect(page.locator("[data-test=pr-pf-preview]")).toContainText("1 changed");

  // Stage it: the digest echoes the preview's, and the run lands in the log.
  await page.locator('[data-action="pr-pf-apply"]').click();
  await expect(page.locator("[data-test=pr-pf-apply-result]")).toContainText("Staged into batch");
  await expect(page.locator("[data-test=pr-pf-runs-count]")).toHaveText("2 runs");
});

test("the low margin tab: the scan, the cleared stamp, and a proposed line graded", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="lowmargin"]').click();

  await expect(page.locator("[data-test=pr-lm-count]")).toHaveText("2 lines");
  const criticalRow = page.locator('tr[data-lm="0c110000-0000-4000-9000-0000000000b2"]');
  await expect(criticalRow).toContainText("SO-2077");
  // UX-106 round 7: the level and the clearance attribution are separate
  // values. The Level cell holds the level's chip, and the "Cleared by" pair
  // holds who cleared it.
  await expect(criticalRow.locator('td[data-label="Level"]')).toContainText("critical");
  await expect(criticalRow.locator('td[data-label="Cleared by"]')).toHaveText("Branch manager (demo)");

  // A proposed line grades through the resolver; the server names the breach.
  await page.locator('[data-picker-search="pr-lm-customer"]').fill("Point Loma");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-lm-customer"]').first().click();
  await page.locator('[data-picker-search="pr-lm-product"]').fill("Wall block");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-lm-product"]').first().click();
  await page.locator('[data-field="pr-lm-qty"]').fill("40");
  await page.locator('[data-field="pr-lm-cost"]').fill("1.90");
  await page.locator('[data-action="pr-lm-grade"]').click();
  await expect(page.locator("[data-test=pr-lm-entries]")).toContainText("critical");
  await expect(page.locator("[data-test=pr-lm-entries]")).toContainText("Tier + category");
});

test("the pricing audit and change log tabs: the log, the bypass named, the change from to", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();

  // The pricing log: the write and the bypass, the bypass naming its reason.
  await page.locator('.pr-tab[data-tab="audit"]').click();
  await expect(page.locator("[data-test=pr-audit-count]")).toHaveText("4 entries");
  const bypassRow = page.locator('tr[data-audit="0e110000-0000-4000-9000-0000000000a2"]');
  await expect(bypassRow).toContainText("Bypassed");
  await expect(bypassRow.locator('[data-audit-reason="0e110000-0000-4000-9000-0000000000a2"]')).toContainText("Approved override");
  const floorRow = page.locator('tr[data-audit="0e110000-0000-4000-9000-0000000000a1"]');
  await expect(floorRow).toContainText("20.00% to 10.00%");

  // The change log: who moved a retail, from what to what, in which batch.
  await page.locator('.pr-tab[data-tab="changelog"]').click();
  await expect(page.locator("[data-test=pr-log-count]")).toHaveText("1 change");
  const logRow = page.locator('tr[data-log="0d110000-0000-4000-9000-0000000000a1"]');
  await expect(logRow).toContainText("retail");
  await expect(logRow).toContainText("$4.80");
  await expect(logRow).toContainText("$5.20");
});
