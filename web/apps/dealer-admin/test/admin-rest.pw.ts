// The admin-rest surfaces' Playwright legs, on the same harness as the app's
// own file: the stub serves the demo routes and each surface's world; the
// scenarios move the world (the stub's own controls) between legs. One file
// per commit's surface, so the registry's next coder lands beside, not into,
// these legs.
import { test, expect } from "@playwright/test";

test.describe("the assist admin surface", () => {
  test("the month's spend lists every active person, zeros included", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="assist"]').click();

    // The report's month and its rows: five staff, four of them at zero, the
    // zero rows rendered as facts and not collapsed away.
    await expect(page.locator("[data-test=assist-month]")).toHaveText("2026-10");
    const rows = page.locator("tr[data-user]");
    await expect(rows).toHaveCount(5);
    const zeroRow = page.locator('tr[data-user="da110000-0000-4000-9000-0000000000yt"]');
    await expect(zeroRow).toContainText("No questions yet");
    // The spend row carries the questions first, tokens second. The dealer
    // admin's own budget resolves from their role's seeded default.
    const adminRow = page.locator('tr[data-user="da110000-0000-4000-9000-000000000001"]');
    await expect(adminRow).toContainText("4 questions");
    await expect(adminRow).toContainText("500 tokens");
    await expect(adminRow).toContainText("500,000, from GENERAL_MANAGER");
    await expect(page.locator('tr[data-user="da110000-0000-4000-9000-000000000002"]')).toContainText("0, their own");
  });

  test("a role budget changes, and the report re-reads with the new number", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // The general manager role's row: change 500,000 to 120,000.
    const roleRow = page.locator("tr[data-role=GENERAL_MANAGER]");
    await expect(roleRow).toContainText("500,000");
    await roleRow.getByRole("button", { name: "Change" }).click();
    await page.locator('[data-field="assist-budget"]').fill("120000");
    await page.locator('[data-action="assist-save"][data-budget-kind="role"]').click();

    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(page.locator("tr[data-role=GENERAL_MANAGER]")).toContainText("120,000");
  });

  test("a person's override is set and reset to their role, whole numbers only", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // A budget that is not a whole number of tokens is refused before any call.
    const yardRow = page.locator('tr[data-user="da110000-0000-4000-9000-0000000000yt"]');
    await yardRow.getByRole("button", { name: "Change" }).click();
    await page.locator('[data-field="assist-budget"]').fill("12.5");
    await page.locator('[data-action="assist-save"][data-budget-kind="user"]').click();
    await expect(page.locator("[data-test=assist-edit-error]")).toContainText("whole number of tokens");

    // A whole number lands as the person's own, and Reset to role appears.
    await page.locator('[data-field="assist-budget"]').fill("250000");
    await page.locator('[data-action="assist-save"][data-budget-kind="user"]').click();
    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(yardRow).toContainText("250,000, their own");

    // Reset to role: the override is gone, the code default applies again.
    await yardRow.getByRole("button", { name: "Reset to role" }).click();
    await expect(yardRow).toContainText("60,000, the default");
    await expect(yardRow.getByRole("button", { name: "Reset to role" })).toHaveCount(0);
  });

  test("the settings carry the four keys and the key's name, and a toggle saves", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // The settings block: the provider, the model, the key variable's NAME,
    // and a no for the key itself, which is never sent.
    await expect(page.locator("[data-test=assist-settings-provider]")).toHaveText("stub");
    await expect(page.locator("[data-test=assist-settings-keyvar]")).toHaveText("HH_ASSIST_API_KEY");
    await expect(page.locator("[data-test=assist-settings-keypresent]")).toHaveText("no");

    // The spend visibility toggle moves, the notice says so, and the
    // checkbox lands on the state the server now answers.
    const toggle = page.locator('#assist-toggle-spend_visible_to_non_admin');
    await expect(toggle).not.toBeChecked();
    await toggle.click();
    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(toggle).toBeChecked();
  });

  test("a frontline caller is refused the assist admin read", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").nth(1).click();
    await page.locator('.surface-link[data-surface="assist"]').click();

    // The assistant's month is leadership's read; the refusal names who may
    // act, and no row is asserted.
    await expect(page.locator("[data-test=assist-error]")).toContainText("leadership");
    await expect(page.locator("[data-test=assist-error]")).toContainText("assistant");
  });
});
