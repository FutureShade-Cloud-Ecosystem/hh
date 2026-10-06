// The just-minted share link: a revoked link takes its address and the "copy
// this now" offer with it, and a page with no clipboard never claims a copy.
import { expect, test, type Page } from "@playwright/test";
import { openTab } from "./surface-tabs";

async function open(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await openTab(page, "admin");
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

const edit = (page: Page) => page.locator("qd-quote-edit");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("revoking the just-minted link clears its address and the copy offer", async ({ page }) => {
  await open(page);
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(edit(page).locator('[data-field="link-url"]')).toBeVisible();
  await expect(edit(page)).toContainText("Copy this now");
  await edit(page).getByRole("button", { name: "Revoke" }).click();
  await expect(edit(page).locator('[data-field="link-url"]')).toHaveCount(0);
  await expect(edit(page)).not.toContainText("Copy this now");
});

test("a page with no clipboard says the link was not copied", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });
  await open(page);
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await edit(page).getByRole("button", { name: "Copy", exact: true }).click();
  await expect(edit(page).getByTestId("qe-error")).toContainText("could not be copied");
  await expect(edit(page)).not.toContainText("Link copied");
});
