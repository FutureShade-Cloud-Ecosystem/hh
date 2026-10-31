// UX-119c: the roles page shows a job shape with nothing curated as one line, opened on demand. The stub curates the
// Sales manager on the Quote Desk (one column); every other shape is on its standard view.
import { test, expect, type Page } from "@playwright/test";

async function openRoleConfig(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
  await page.locator('.surface-link[data-surface="role-config"]').click();
  await expect(page.locator("[data-test=config-roles-count]")).toBeVisible();
}

const toggle = (page: Page, profile: string) => page.locator(`[data-test="config-shape-toggle-${profile}"]`);
const tiles = (page: Page, profile: string) => page.locator(`tr[data-profile="${profile}"] [data-test^=config-cell-]`);

test("an uncurated shape is one row until asked, and a curated shape starts open", async ({ page }) => {
  await openRoleConfig(page);

  // Yard: nothing curated, one line, no tiles.
  await expect(page.locator('[data-test="config-shape-row-yard"]')).toContainText("Standard view on all 10 surfaces");
  await expect(toggle(page, "yard")).toHaveText("Show surfaces");
  await expect(toggle(page, "yard")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toBeHidden();

  await toggle(page, "yard").click();
  await expect(toggle(page, "yard")).toHaveText("Hide surfaces");
  await expect(toggle(page, "yard")).toHaveAttribute("aria-expanded", "true");
  await expect(toggle(page, "yard")).toBeFocused();
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toHaveText("Standard view");
  await expect(tiles(page, "yard")).toHaveCount(10);
  // Only that shape opened.
  await expect(page.locator('[data-test="config-cell-counter-till-driver"]')).toBeHidden();

  await toggle(page, "yard").click();
  await expect(toggle(page, "yard")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator('[data-test="config-cell-counter-till-yard"]')).toBeHidden();

  // Sales manager: curated, open, its curated tile marked, the other nine on the standard view.
  await expect(page.locator('[data-test="config-shape-row-manager.sales"]')).toContainText("1 of 10 surfaces curated");
  await expect(toggle(page, "manager.sales")).toHaveAttribute("aria-expanded", "true");
  await expect(tiles(page, "manager.sales")).toHaveCount(10);
  await expect(page.locator('[data-test="config-cell-quote-desk-manager.sales"]')).toHaveText("1 column");
  await expect(page.locator('[data-test="config-cell-counter-till-manager.sales"]')).toHaveText("Standard view");
});

test("Open all opens every shape and Close all closes the uncurated ones", async ({ page }) => {
  await openRoleConfig(page);
  const all = page.locator('[data-test="config-toggle-all"]');
  await expect(all).toHaveText("Open all");

  await all.click();
  await expect(all).toHaveText("Close all");
  await expect(page.locator("[data-test^=config-cell-]")).toHaveCount(130);
  for (const profile of ["counter", "yard", "manager.general", "manager.sales"]) {
    await expect(toggle(page, profile)).toHaveAttribute("aria-expanded", "true");
  }
  await expect(all).toBeFocused();

  await all.click();
  await expect(all).toHaveText("Open all");
  await expect(toggle(page, "counter")).toHaveAttribute("aria-expanded", "false");
  await expect(toggle(page, "yard")).toHaveAttribute("aria-expanded", "false");
  await expect(toggle(page, "manager.general")).toHaveAttribute("aria-expanded", "false");
  await expect(toggle(page, "manager.sales")).toHaveAttribute("aria-expanded", "true");
  await expect(tiles(page, "manager.sales")).toHaveCount(10);
  await expect(page.locator('[data-test="config-cell-counter-till-counter"]')).toBeHidden();
});
