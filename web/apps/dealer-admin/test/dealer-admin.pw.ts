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
