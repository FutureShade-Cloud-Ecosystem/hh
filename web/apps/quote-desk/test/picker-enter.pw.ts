// Round 10 item 23 (row 1092): a lone customer match accepts Enter. With several matches and none highlighted,
// Enter still picks nobody: the person chooses, the picker never guesses an account.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function toPicker(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  return picker.getByPlaceholder("Search name or account #…");
}

test("one match and Enter picks the customer", async ({ page }) => {
  const box = await toPicker(page);
  await box.fill("Quarry");
  await expect(page.getByRole("option")).toHaveCount(1);
  await box.press("Enter");
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  await expect(page.getByTestId("qb-identity")).toContainText("Quarry Lane Paving");
});

test("several matches and Enter with none highlighted picks nobody", async ({ page }) => {
  const box = await toPicker(page);
  await box.fill("Co");
  await expect(page.getByRole("option").nth(1)).toBeVisible();
  await box.press("Enter");
  await expect(page.getByRole("option").first()).toBeVisible();
  await expect(page.getByTestId("qb-identity")).not.toContainText("MR");
});
