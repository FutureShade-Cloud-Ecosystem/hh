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

  // The trail is an admin read; the refusal names who may act.
  await expect(page.locator("[data-test=audit-error]")).toContainText("managers and above");
});

test("the pricing surface: the margin policies, and an edit saved clean", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();

  // The strip names the twelve tabs; the margin tab is the open one.
  await expect(page.locator(".pr-tab")).toHaveCount(12);
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
  await expect(page.locator('tr[data-rule="9a110000-0000-4000-9000-0000000000a1"]')).toContainText("retired");
});

test("the customer prices tab: one account's overrides, an add, and a delete asked twice", async ({ page }) => {
  await page.goto("/");
  const cards = page.locator("[data-flow]");
  await cards.first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="customers"]').click();

  // Before a customer is named, the empty says what this tab is for.
  await expect(page.locator("[data-test=pr-cust-empty]")).toContainText("Type a customer id");

  // Load the yard account: one price on Retaining Walls, named by category.
  await page.locator('[data-field="pr-cust-id"]').fill("cu110000-0000-4000-9000-0000000000a1");
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
  await expect(page.locator('tr[data-break="7a110000-0000-4000-9000-0000000000a1"]')).toContainText("retired");
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
  await expect(page.locator("[data-test=pr-ledger-total]")).toContainText("CA$140.00");
  await expect(page.locator("[data-accrual]")).toHaveCount(1);

  // An accrual: the facts in, the server's counts out, and the ledger re-read.
  await page.locator('[data-field="pr-acc-source-id"]').fill("0f110000-0000-4000-9000-0000000000a2");
  await page.locator('[data-field="pr-acc-product"]').fill("fa110000-0000-4000-9000-0000000000a1");
  await page.locator('[data-field="pr-acc-category"]').fill("ca7a0000-0000-4000-9000-0000000000a1");
  await page.locator('[data-field="pr-acc-qty"]').fill("6");
  await page.locator('[data-field="pr-acc-sale"]').fill("3500.00");
  await page.locator('[data-field="pr-acc-cost"]').fill("2500.00");
  await page.locator('[data-action="pr-accrue"]').click();
  await expect(page.locator("[data-test=pr-accrue-result]")).toContainText("1 accrual written");
  await expect(page.locator("[data-test=pr-ledger-total]")).toContainText("CA$210.00");
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
  await page.locator('[data-field="pr-uom-probe-product"]').fill("fa110000-0000-4000-9000-0000000000a1");
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
  await expect(criticalRow).toContainText("cleared: Branch manager (demo)");

  // A proposed line grades through the resolver; the server names the breach.
  await page.locator('[data-field="pr-lm-customer"]').fill("cu110000-0000-4000-9000-0000000000a1");
  await page.locator('[data-field="pr-lm-product"]').fill("fa110000-0000-4000-9000-0000000000a2");
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
  await expect(page.locator("[data-test=pr-audit-count]")).toHaveText("2 entries");
  const bypassRow = page.locator('tr[data-audit="0e110000-0000-4000-9000-0000000000a2"]');
  await expect(bypassRow).toContainText("Bypassed");
  await expect(page.locator('tr[data-audit-reason="0e110000-0000-4000-9000-0000000000a2"]')).toContainText("APPROVED_OVERRIDE");
  const floorRow = page.locator('tr[data-audit="0e110000-0000-4000-9000-0000000000a1"]');
  await expect(floorRow).toContainText("2000 to 1000");

  // The change log: who moved a retail, from what to what, in which batch.
  await page.locator('.pr-tab[data-tab="changelog"]').click();
  await expect(page.locator("[data-test=pr-log-count]")).toHaveText("1 change");
  const logRow = page.locator('tr[data-log="0d110000-0000-4000-9000-0000000000a1"]');
  await expect(logRow).toContainText("retail");
  await expect(logRow).toContainText("CA$4.80");
  await expect(logRow).toContainText("CA$5.20");
});
